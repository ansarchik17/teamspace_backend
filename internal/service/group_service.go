package service

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/i18n"
	"github.com/west6ide/workspace-backend/internal/repository"
	"github.com/west6ide/workspace-backend/internal/websocket"
	"go.uber.org/zap"
)

type GroupService struct {
	groupRepo           *repository.GroupRepository
	calendarRepo        *repository.CalendarRepository
	messageRepo         *repository.MessageRepository
	pinnedRepo          *repository.PinnedRepository
	reactionRepo        *repository.ReactionRepository
	calendarService     *CalendarService
	userRepo            *repository.UserRepository
	fileService         *FileService
	hub                 *websocket.Hub
	notificationService *NotificationService
	gamification        *GamificationService // новое поле
	eventPublisher      EventPublisher
	logger              *zap.Logger
}

type groupPayload struct {
	GroupID    uuid.UUID `json:"group_id"`
	CreatedBy  int64     `json:"created_by"`
	Name       string    `json:"name"`
	Type       string    `json:"type"`
	MaxMembers int       `json:"max_members"`
}

func NewGroupService(
	groupRepo *repository.GroupRepository,
	calendarRepo *repository.CalendarRepository,
	messageRepo *repository.MessageRepository,
	pinnedRepo *repository.PinnedRepository,
	reactionRepo *repository.ReactionRepository,
	calendarService *CalendarService,
	userRepo *repository.UserRepository,
	fileService *FileService,
	hub *websocket.Hub,
	notificationService *NotificationService,
	gamification *GamificationService,
	eventPublisher EventPublisher,
	logger *zap.Logger,
) *GroupService {
	return &GroupService{
		groupRepo:           groupRepo,
		calendarRepo:        calendarRepo,
		messageRepo:         messageRepo,
		pinnedRepo:          pinnedRepo,
		reactionRepo:        reactionRepo,
		calendarService:     calendarService,
		userRepo:            userRepo,
		fileService:         fileService,
		hub:                 hub,
		notificationService: notificationService,
		gamification:        gamification,
		eventPublisher:      eventPublisher,
		logger:              logger.With(zap.String("service", "group")),
	}
}

func (s *GroupService) CreateGroupEvent(ctx context.Context, groupID uuid.UUID, userID int64, req *domain.CreateEventRequest, force bool) (*domain.Event, error) {
	isMember, err := s.groupRepo.IsMember(ctx, groupID, userID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to check membership", err)
	}
	if !isMember {
		return nil, apperrors.NotGroupMember()
	}
	if s.calendarService == nil || s.calendarRepo == nil {
		return nil, apperrors.Internal("group calendar service is not configured")
	}

	memberIDs, err := s.groupRepo.GetGroupMembers(ctx, groupID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to get group members", err)
	}

	attendeeIDs := make([]int64, 0, len(memberIDs))
	for _, memberID := range memberIDs {
		if memberID == userID {
			continue
		}
		attendeeIDs = append(attendeeIDs, memberID)
	}

	createReq := *req
	createReq.AttendeeIDs = attendeeIDs

	event, err := s.calendarService.CreateEventWithOptions(ctx, userID, &createReq, force)
	if err != nil {
		return nil, err
	}

	if err := s.calendarRepo.LinkEventToGroup(ctx, groupID, event.ID, userID); err != nil {
		return nil, apperrors.InternalWithErr("failed to link event to group", err)
	}

	return event, nil
}

func (s *GroupService) ListUpcomingGroupEvents(ctx context.Context, groupID uuid.UUID, userID int64, limit int) (*domain.GroupEventsResponse, error) {
	isMember, err := s.groupRepo.IsMember(ctx, groupID, userID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to check membership", err)
	}
	if !isMember {
		return nil, apperrors.NotGroupMember()
	}
	if s.calendarRepo == nil {
		return nil, apperrors.Internal("group calendar repository is not configured")
	}

	events, err := s.calendarRepo.ListUpcomingByGroup(ctx, groupID, time.Now().UTC(), limit)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to load group events", err)
	}

	return &domain.GroupEventsResponse{
		Count:  len(events),
		Events: events,
	}, nil
}

// CreateGroup creates a new group
// isGroupAdminRole reports whether the given role string has admin-level access.
func isGroupAdminRole(role string) bool {
	return role == "owner" || role == "admin"
}

func validateGroupMemberRemoval(actorID, targetID int64, actorRole, targetRole string) error {
	if actorID == targetID {
		return apperrors.Forbidden("use the leave-group endpoint to remove yourself")
	}
	if targetRole == "owner" {
		return apperrors.Forbidden("cannot remove group owner")
	}
	if actorRole == "admin" && targetRole == "admin" {
		return apperrors.Forbidden("only owner can remove another admin")
	}
	return nil
}

func validateGroupRoleChange(actorID, targetID int64, targetRole string) error {
	if actorID == targetID || targetRole == "owner" {
		return apperrors.Forbidden("group owner role cannot be changed")
	}
	return nil
}

// clampGroupMaxMembers returns the default (200) when n is out of bounds.
func clampGroupMaxMembers(n int) int {
	if n <= 0 || n > 10000 {
		return 200
	}
	return n
}

func (s *GroupService) CreateGroup(ctx context.Context, creatorID int64, name, description, groupType string, maxMembers int) (*domain.Group, error) {
	if name == "" {
		return nil, apperrors.BadRequest("group name cannot be empty")
	}

	if maxMembers <= 0 || maxMembers > 10000 {
		maxMembers = 200 // Default
	}

	group := &domain.Group{
		ID:          uuid.New(),
		Name:        name,
		Description: &description,
		Type:        groupType,
		MaxMembers:  maxMembers,
		CreatedBy:   creatorID,
		IsActive:    true,
	}

	if err := s.groupRepo.CreateGroup(ctx, group); err != nil {
		return nil, apperrors.InternalWithErr("failed to create group", err)
	}

	// Add creator as owner
	if err := s.groupRepo.AddMember(ctx, group.ID, creatorID, "owner"); err != nil {
		return nil, apperrors.InternalWithErr("failed to add creator as owner", err)
	}

	// Гамификация: создание группы
	if s.gamification != nil {
		go func() { _ = s.gamification.TrackActivity(context.Background(), creatorID, domain.ActionGroupCreated) }()
	}

	s.logger.Info("Group created",
		zap.String("group_id", group.ID.String()),
		zap.Int64("creator_id", creatorID),
	)

	publishEventAsync(s.logger, s.eventPublisher, "chat.group.created", groupPayload{
		GroupID:    group.ID,
		CreatedBy:  creatorID,
		Name:       group.Name,
		Type:       group.Type,
		MaxMembers: group.MaxMembers,
	}, zap.String("group_id", group.ID.String()))

	return group, nil
}

// GetGroup gets group by ID
// GetGroup gets group by ID with members
func (s *GroupService) GetGroup(ctx context.Context, groupID uuid.UUID, userID int64) (*domain.GroupDetails, error) {
	// Check if user is member
	isMember, err := s.groupRepo.IsMember(ctx, groupID, userID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to check membership", err)
	}
	if !isMember {
		return nil, apperrors.NotGroupMember()
	}

	group, err := s.groupRepo.GetByID(ctx, groupID)
	if err != nil {
		return nil, apperrors.GroupNotFound()
	}

	members, err := s.groupRepo.GetGroupMemberDetails(ctx, groupID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to get group members", err)
	}

	// Inject real-time online status
	onlineStatus := domain.StatusOnline
	for i := range members {
		if s.hub.IsUserOnline(members[i].ID) {
			members[i].AvailabilityStatus = &onlineStatus
		}
	}

	return &domain.GroupDetails{
		Group:       *group,
		Members:     members,
		MemberCount: len(members),
	}, nil
}

func (s *GroupService) GetGroupMemberIDs(ctx context.Context, groupID uuid.UUID, userID int64) ([]int64, error) {
	isMember, err := s.groupRepo.IsMember(ctx, groupID, userID)
	if err != nil {
		return nil, apperrors.NotGroupMember()
	}
	if !isMember {
		return nil, apperrors.NotGroupMember()
	}

	members, err := s.groupRepo.GetGroupMembers(ctx, groupID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to get group members", err)
	}

	return members, nil
}

// UpdateGroup updates group
func (s *GroupService) UpdateGroup(ctx context.Context, groupID uuid.UUID, userID int64, name, description string) error {
	// Check if user is admin
	role, err := s.groupRepo.GetMemberRole(ctx, groupID, userID)
	if err != nil {
		return apperrors.NotGroupMember()
	}
	if role != "owner" && role != "admin" {
		return apperrors.NotGroupAdmin()
	}

	group, err := s.groupRepo.GetByID(ctx, groupID)
	if err != nil {
		return apperrors.GroupNotFound()
	}

	if name != "" {
		group.Name = name
	}
	if description != "" {
		group.Description = &description
	}

	if err := s.groupRepo.Update(ctx, group); err != nil {
		return apperrors.InternalWithErr("failed to update group", err)
	}

	s.logger.Info("Group updated",
		zap.String("group_id", groupID.String()),
		zap.Int64("user_id", userID),
	)

	return nil
}

// UpdateGroupAvatar updates group avatar
func (s *GroupService) UpdateGroupAvatar(ctx context.Context, groupID uuid.UUID, userID int64, file io.ReadCloser, header *multipart.FileHeader) (*domain.Group, error) {
	// Check if user is admin
	role, err := s.groupRepo.GetMemberRole(ctx, groupID, userID)
	if err != nil {
		return nil, apperrors.NotGroupMember()
	}
	if role != "owner" && role != "admin" {
		return nil, apperrors.NotGroupAdmin()
	}

	// Upload file
	objectName, err := s.fileService.UploadFile(ctx, userID, header.Filename, "group_avatar", header.Size, file)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to upload avatar", err)
	}

	// Get full URL
	avatarURL, err := s.fileService.GetFileURL(ctx, objectName, 0)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to get avatar url", err)
	}

	// Update group
	group, err := s.groupRepo.GetByID(ctx, groupID)
	if err != nil {
		return nil, apperrors.GroupNotFound()
	}

	group.AvatarURL = &avatarURL

	if err := s.groupRepo.Update(ctx, group); err != nil {
		return nil, apperrors.InternalWithErr("failed to update group avatar", err)
	}

	s.logger.Info("Group avatar updated",
		zap.String("group_id", groupID.String()),
		zap.Int64("user_id", userID),
	)

	return group, nil
}

// DeleteGroup deletes a group
func (s *GroupService) DeleteGroup(ctx context.Context, groupID uuid.UUID, userID int64) error {
	// Check if user is owner
	role, err := s.groupRepo.GetMemberRole(ctx, groupID, userID)
	if err != nil {
		return apperrors.NotGroupMember()
	}
	if role != "owner" {
		return apperrors.Forbidden("only owner can delete group")
	}

	if err := s.groupRepo.DeleteGroup(ctx, groupID); err != nil {
		return apperrors.InternalWithErr("failed to delete group", err)
	}

	// Remove all pins for this group (for all users)
	if err := s.pinnedRepo.RemoveAllPinsForChat(ctx, groupID, "group"); err != nil {
		s.logger.Warn("Failed to remove pins for deleted group",
			zap.String("group_id", groupID.String()),
			zap.Error(err),
		)
	}

	s.logger.Info("Group deleted",
		zap.String("group_id", groupID.String()),
		zap.Int64("user_id", userID),
	)

	return nil
}

// AddMember adds a member to group
func (s *GroupService) AddMember(ctx context.Context, groupID uuid.UUID, userID, newMemberID int64) error {
	// Check if user is admin
	role, err := s.groupRepo.GetMemberRole(ctx, groupID, userID)
	if err != nil {
		return apperrors.NotGroupMember()
	}
	if role != "owner" && role != "admin" {
		return apperrors.NotGroupAdmin()
	}

	// Check group size
	count, err := s.groupRepo.GetMemberCount(ctx, groupID)
	if err != nil {
		return apperrors.InternalWithErr("failed to get member count", err)
	}

	group, err := s.groupRepo.GetByID(ctx, groupID)
	if err != nil {
		return apperrors.GroupNotFound()
	}

	if count >= group.MaxMembers {
		return apperrors.GroupFull()
	}

	// Add member
	if err := s.groupRepo.AddMember(ctx, groupID, newMemberID, "member"); err != nil {
		return apperrors.InternalWithErr("failed to add member", err)
	}

	s.logger.Info("Member added to group",
		zap.String("group_id", groupID.String()),
		zap.Int64("new_member_id", newMemberID),
	)

	// Find added member details
	addedUser, err := s.userRepo.GetByID(ctx, newMemberID)
	var addedName string
	if err == nil {
		addedName = addedUser.FirstName + " " + addedUser.LastName
	} else {
		addedName = fmt.Sprintf("Пользователь %d", newMemberID)
	}

	// Find actor user details
	actorUser, err := s.userRepo.GetByID(ctx, userID)
	var actorName string
	if err == nil {
		actorName = actorUser.FirstName + " " + actorUser.LastName
	} else {
		actorName = fmt.Sprintf("Пользователь %d", userID)
	}

	systemText := actorName + " добавил " + addedName + " в группу"
	metadataStr := `{"is_service":true,"system_type":"add_member"}`
	serviceMsg := &domain.Message{
		ID:             uuid.New(),
		ConversationID: groupID,
		SenderID:       userID,
		Content:        &systemText,
		Type:           "text",
		Metadata:       &metadataStr,
	}

	// Create and persist the service message
	if err := s.messageRepo.Create(ctx, serviceMsg); err != nil {
		s.logger.Error("AddMember: failed to create service message", zap.Error(err))
	}

	// Broadcast the service message notification to all group members (which now includes the new member)
	messageView, err := s.messageRepo.GetByID(ctx, serviceMsg.ID)
	if err == nil {
		members, err := s.groupRepo.GetGroupMembers(ctx, groupID)
		if err == nil {
			for _, memberID := range members {
				if s.hub.IsUserOnline(memberID) {
					s.hub.SendMessageNotification(memberID, messageView, false)
				}
			}
		} else {
			s.logger.Error("AddMember: failed to get group members", zap.Error(err))
		}
	} else {
		s.logger.Error("AddMember: failed to get service message view", zap.Error(err))
	}

	// Also send the legacy added_to_group WS notification to the added member for backup compatibility
	if s.hub != nil {
		notification := &websocket.Message{
			Type:           "added_to_group",
			To:             newMemberID,
			ConversationID: &groupID,
			Title:          "Added to group",
			Body:           "You have been added to a new group",
			Category:       "group",
			Timestamp:      time.Now().Unix(),
			Content: map[string]interface{}{
				"group_id": groupID.String(),
				"user_id":  newMemberID,
				"action":   "added",
			},
		}
		s.hub.SendToUser(newMemberID, notification)
	}

	return nil
}

// RemoveMember removes a member from group
func (s *GroupService) RemoveMember(ctx context.Context, groupID uuid.UUID, userID, targetMemberID int64) error {
	// Check if user is admin
	role, err := s.groupRepo.GetMemberRole(ctx, groupID, userID)
	if err != nil {
		return apperrors.NotGroupMember()
	}
	if role != "owner" && role != "admin" {
		return apperrors.NotGroupAdmin()
	}

	targetRole, err := s.groupRepo.GetMemberRole(ctx, groupID, targetMemberID)
	if err != nil {
		return apperrors.NotFound("target member not found")
	}
	if err := validateGroupMemberRemoval(userID, targetMemberID, role, targetRole); err != nil {
		return err
	}

	// Find target member details
	targetUser, err := s.userRepo.GetByID(ctx, targetMemberID)
	var targetName string
	if err == nil {
		targetName = targetUser.FirstName + " " + targetUser.LastName
	} else {
		targetName = fmt.Sprintf("Пользователь %d", targetMemberID)
	}

	// Find actor user details
	actorUser, err := s.userRepo.GetByID(ctx, userID)
	var actorName string
	if err == nil {
		actorName = actorUser.FirstName + " " + actorUser.LastName
	} else {
		actorName = fmt.Sprintf("Пользователь %d", userID)
	}

	systemText := actorName + " удалил " + targetName + " из группы"
	metadataStr := `{"is_service":true,"system_type":"remove_member"}`
	serviceMsg := &domain.Message{
		ID:             uuid.New(),
		ConversationID: groupID,
		SenderID:       userID,
		Content:        &systemText,
		Type:           "text",
		Metadata:       &metadataStr,
	}

	// Get all group members before removal so everyone (including removed member) gets the socket update
	members, getMembersErr := s.groupRepo.GetGroupMembers(ctx, groupID)

	if err := s.groupRepo.RemoveMember(ctx, groupID, targetMemberID); err != nil {
		return apperrors.InternalWithErr("failed to remove member", err)
	}

	// Create and persist the service message
	if err := s.messageRepo.Create(ctx, serviceMsg); err != nil {
		s.logger.Error("RemoveMember: failed to create service message", zap.Error(err))
	}

	s.logger.Info("Member removed from group",
		zap.String("group_id", groupID.String()),
		zap.Int64("removed_member_id", targetMemberID),
	)

	// Broadcast the service message notification to all members
	messageView, err := s.messageRepo.GetByID(ctx, serviceMsg.ID)
	if err == nil && getMembersErr == nil {
		for _, memberID := range members {
			if s.hub.IsUserOnline(memberID) {
				s.hub.SendMessageNotification(memberID, messageView, false)
			}
		}
	} else {
		if err != nil {
			s.logger.Error("RemoveMember: failed to get service message view", zap.Error(err))
		}
		if getMembersErr != nil {
			s.logger.Error("RemoveMember: failed to get members before removal", zap.Error(getMembersErr))
		}
	}

	return nil
}

// LeaveGroup allows user to leave group
func (s *GroupService) LeaveGroup(ctx context.Context, groupID uuid.UUID, userID int64) error {
	// Check if user is member
	isMember, err := s.groupRepo.IsMember(ctx, groupID, userID)
	if err != nil {
		return apperrors.InternalWithErr("failed to check membership", err)
	}
	if !isMember {
		return apperrors.NotGroupMember()
	}

	// Owner cannot leave (must transfer ownership or delete group)
	role, err := s.groupRepo.GetMemberRole(ctx, groupID, userID)
	if err != nil {
		return apperrors.InternalWithErr("failed to get role", err)
	}
	if role == "owner" {
		return apperrors.Forbidden("owner cannot leave group without transferring ownership")
	}

	// Find user details for system message
	user, err := s.userRepo.GetByID(ctx, userID)
	var userName string
	if err == nil {
		userName = user.FirstName + " " + user.LastName
	} else {
		userName = fmt.Sprintf("Пользователь %d", userID)
	}

	systemText := userName + " вышел из группы"
	metadataStr := `{"is_service":true,"system_type":"leave_group"}`
	serviceMsg := &domain.Message{
		ID:             uuid.New(),
		ConversationID: groupID,
		SenderID:       userID,
		Content:        &systemText,
		Type:           "text",
		Metadata:       &metadataStr,
	}

	// Get all group members before leaving so everyone (including leaving member) gets the socket update
	members, getMembersErr := s.groupRepo.GetGroupMembers(ctx, groupID)

	if err := s.groupRepo.RemoveMember(ctx, groupID, userID); err != nil {
		return apperrors.InternalWithErr("failed to leave group", err)
	}

	// Create and persist the service message
	if err := s.messageRepo.Create(ctx, serviceMsg); err != nil {
		s.logger.Error("LeaveGroup: failed to create service message", zap.Error(err))
	}

	s.logger.Info("User left group",
		zap.String("group_id", groupID.String()),
		zap.Int64("user_id", userID),
	)

	// Broadcast the service message notification to all members
	messageView, err := s.messageRepo.GetByID(ctx, serviceMsg.ID)
	if err == nil && getMembersErr == nil {
		for _, memberID := range members {
			if s.hub.IsUserOnline(memberID) {
				s.hub.SendMessageNotification(memberID, messageView, false)
			}
		}
	} else {
		if err != nil {
			s.logger.Error("LeaveGroup: failed to get service message view", zap.Error(err))
		}
		if getMembersErr != nil {
			s.logger.Error("LeaveGroup: failed to get members before leaving", zap.Error(getMembersErr))
		}
	}

	return nil
}

// UpdateMemberRole updates member role
func (s *GroupService) UpdateMemberRole(ctx context.Context, groupID uuid.UUID, userID, targetMemberID int64, newRole string) error {
	// Only owner can change roles
	role, err := s.groupRepo.GetMemberRole(ctx, groupID, userID)
	if err != nil {
		return apperrors.NotGroupMember()
	}
	if role != "owner" {
		return apperrors.Forbidden("only owner can change member roles")
	}

	// Validate role
	if newRole != "member" && newRole != "admin" {
		return apperrors.BadRequest("invalid role")
	}
	targetRole, err := s.groupRepo.GetMemberRole(ctx, groupID, targetMemberID)
	if err != nil {
		return apperrors.NotFound("target member not found")
	}
	if err := validateGroupRoleChange(userID, targetMemberID, targetRole); err != nil {
		return err
	}

	if err := s.groupRepo.UpdateMemberRole(ctx, groupID, targetMemberID, newRole); err != nil {
		return apperrors.InternalWithErr("failed to update role", err)
	}

	s.logger.Info("Member role updated",
		zap.String("group_id", groupID.String()),
		zap.Int64("target_member_id", targetMemberID),
		zap.String("new_role", newRole),
	)

	return nil
}

// GetUserGroups gets all groups for user
func (s *GroupService) GetUserGroups(ctx context.Context, userID int64, limit, offset int) ([]*domain.GroupView, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	groups, err := s.groupRepo.GetUserGroups(ctx, userID, limit, offset)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to get groups", err)
	}

	return groups, nil
}

// SendMessage sends a message in group
func (s *GroupService) SendMessage(ctx context.Context, groupID uuid.UUID, senderID int64, content, contentHash, messageType string, metadata *string, replyToID *uuid.UUID, attachment *MessageAttachmentInput, senderSessionKey string) (*domain.MessageView, error) {
	// Check if user is member
	isMember, err := s.groupRepo.IsMember(ctx, groupID, senderID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to check membership", err)
	}
	if !isMember {
		return nil, apperrors.NotGroupMember()
	}

	attachmentURL, attachmentType, attachmentSize, err := normalizeMessageAttachment(messageType, attachment)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(content) == "" && attachmentURL == nil {
		return nil, apperrors.BadRequest("message content cannot be empty")
	}
	if strings.TrimSpace(messageType) == "" {
		messageType = inferMessageType(attachmentType)
	}

	// Проверяем, что сообщение, на которое отвечают, существует и принадлежит этой же группе
	if replyToID != nil {
		replyToMessage, err := s.messageRepo.GetByID(ctx, *replyToID)
		if err != nil {
			return nil, apperrors.BadRequest("replied message not found")
		}
		if replyToMessage.ConversationID != groupID {
			return nil, apperrors.BadRequest("replied message does not belong to this group")
		}
	}

	// ВАЖНО: При отправке сообщения автоматически восстанавливаем группу у ВСЕХ участников
	// Включая отправителя! Если он отправляет в скрытую группу - группа должна появиться у него тоже
	members, err := s.groupRepo.GetGroupMembers(ctx, groupID)
	if err == nil {
		for _, memberID := range members {
			// Unhide для ВСЕХ участников (включая отправителя!)
			_ = s.groupRepo.UnhideGroup(ctx, groupID, memberID)
		}
	}

	// Create message (using group ID as conversation ID)
	message := &domain.Message{
		ID:             uuid.New(),
		ConversationID: groupID,

		SenderID:       senderID,
		ReplyToID:      replyToID, // Устанавливаем reply_to_id
		Content:        &content,
		ContentHash:    &contentHash,
		Type:           messageType,
		AttachmentURL:  attachmentURL,
		AttachmentType: attachmentType,
		AttachmentSize: attachmentSize,
		AttachmentName: attachmentName(attachment),
		Metadata:       metadata,
	}

	if err := s.messageRepo.Create(ctx, message); err != nil {
		return nil, apperrors.InternalWithErr("failed to create message", err)
	}

	publishEventAsync(s.logger, s.eventPublisher, "chat.group.message.sent", chatMessagePayload{
		MessageID:      message.ID,
		ConversationID: groupID,
		SenderID:       senderID,
		Type:           message.Type,
		ReplyToID:      message.ReplyToID,
		AttachmentURL:  message.AttachmentURL,
		AttachmentType: message.AttachmentType,
	}, zap.String("message_id", message.ID.String()), zap.String("group_id", groupID.String()))

	messageView, err := s.messageRepo.GetByID(ctx, message.ID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to get message", err)
	}

	s.logger.Info("Group message sent",
		zap.String("group_id", groupID.String()),
		zap.String("message_id", message.ID.String()),
		zap.Int64("sender_id", senderID),
	)

	// Гамификация: активность в мессенджере — 1 балл раз в день
	if s.gamification != nil {
		go func() {
			switch messageType {
			case "file", "image", "audio", "video", "voice", "video_message":
				_ = s.gamification.TrackActivityOncePerDay(context.Background(), senderID, domain.ActionFileSentInChat)
			default:
				_ = s.gamification.TrackActivityOncePerDay(context.Background(), senderID, domain.ActionMessageSent)
			}
		}()
	}

	mentionedUserIDs := mentionedUserIDsFromMetadata(ctx, s.userRepo, metadata)
	if len(mentionedUserIDs) > 0 {
		memberSet := make(map[int64]bool, len(members))
		for _, memberID := range members {
			memberSet[memberID] = true
		}

		mentionIDs := make([]int64, 0, len(mentionedUserIDs))
		for mentionID := range mentionedUserIDs {
			if memberSet[mentionID] && mentionID != senderID {
				mentionIDs = append(mentionIDs, mentionID)
			} else {
				delete(mentionedUserIDs, mentionID)
			}
		}
		if len(mentionIDs) > 0 {
			_ = s.messageRepo.CreateMentions(ctx, message.ID, mentionIDs)

			// ВАЖНО: Если пользователя тегнули, группа должна восстановиться у него (unhide)
			for _, mentionID := range mentionIDs {
				_ = s.groupRepo.UnhideGroup(ctx, groupID, mentionID)
			}
		}
	}

	muteMap, err := s.groupRepo.GetMemberMuteMap(ctx, groupID)
	if err != nil {
		s.logger.Warn("Failed to load group mute settings", zap.Error(err))
		muteMap = map[int64]bool{}
	}

	nonSenderIDs := make([]int64, 0, len(members))
	for _, mid := range members {
		if mid != senderID {
			nonSenderIDs = append(nonSenderIDs, mid)
		}
	}
	if len(nonSenderIDs) > 0 {
		_ = s.messageRepo.BulkCreateMessageStatus(ctx, message.ID, nonSenderIDs)
	}

	for _, memberID := range members {
		if s.hub.IsUserOnline(memberID) {
			if memberID == senderID {
				s.hub.SendMessageNotificationExceptSession(memberID, messageView, mentionedUserIDs[memberID], senderSessionKey)
			} else if s.hub.SendMessageNotification(memberID, messageView, mentionedUserIDs[memberID]) > 0 {
				changed, deliveryErr := s.messageRepo.MarkAsDelivered(ctx, message.ID, memberID)
				if deliveryErr != nil {
					s.logger.Warn("failed to mark group websocket delivery", zap.String("message_id", message.ID.String()), zap.Int64("recipient_id", memberID), zap.Error(deliveryErr))
				} else if changed {
					deliveredCount, _, countErr := s.messageRepo.GetStatusCounts(ctx, message.ID)
					if countErr != nil {
						s.logger.Warn("failed to load group delivery count", zap.String("message_id", message.ID.String()), zap.Error(countErr))
					} else {
						s.hub.SendDeliveryReceipt(senderID, memberID, message.ID, groupID, deliveredCount)
					}
				}
			}
		}

		if memberID == senderID {
			continue
		}

		if muteMap[memberID] && !mentionedUserIDs[memberID] {
			continue
		}

		notificationLocale := s.notificationService.recipientLocale(ctx, memberID)
		title := i18n.SystemNotification(notificationLocale, "New Group Message")
		notificationBody := i18n.SystemNotification(notificationLocale, "Новое сообщение в группе")

		if mentionedUserIDs[memberID] {
			title = i18n.SystemNotification(notificationLocale, "Упоминание в группе")
			notificationBody = i18n.SystemNotification(notificationLocale, "Вас упомянули в сообщении")
			if content != "" && len(content) < 100 {
				notificationBody = content
			}
		} else {
			switch messageType {
			case "image":
				notificationBody = i18n.SystemNotification(notificationLocale, "Новое фото в группе")
			case "video":
				notificationBody = i18n.SystemNotification(notificationLocale, "Новое видео в группе")
			case "file":
				notificationBody = i18n.SystemNotification(notificationLocale, "Новый файл в группе")
			case "audio":
				notificationBody = i18n.SystemNotification(notificationLocale, "Новое аудио в группе")
			case "voice":
				notificationBody = i18n.SystemNotification(notificationLocale, "Голосовое сообщение в группе")
			case "video_message":
				notificationBody = i18n.SystemNotification(notificationLocale, "Видеосообщение в группе")
			}
		}

		notificationData := map[string]string{
			"group_id":   groupID.String(),
			"message_id": message.ID.String(),
			"type":       message.Type,
		}

		if mentionedUserIDs[memberID] {
			notificationData["is_mention"] = "true"
		}

		err := s.notificationService.SendPushNotification(memberID, title, notificationBody, notificationData)
		if err != nil {
			s.logger.Error("Failed to send push notification", zap.Error(err))
		}
	}

	return messageView, nil
}

func (s *GroupService) MuteGroup(ctx context.Context, groupID uuid.UUID, userID int64, mute bool) (*domain.GroupView, error) {
	isMember, err := s.groupRepo.IsMember(ctx, groupID, userID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to verify member", err)
	}
	if !isMember {
		return nil, apperrors.NotGroupMember()
	}

	if err := s.groupRepo.SetMute(ctx, groupID, userID, mute); err != nil {
		return nil, apperrors.InternalWithErr("failed to update mute setting", err)
	}

	return s.groupRepo.GetGroupView(ctx, groupID, userID)
}

// GetGroupMessages gets messages from group
func (s *GroupService) GetGroupMessages(ctx context.Context, groupID uuid.UUID, userID int64, currentDeviceID string, limit, offset int) ([]*domain.MessageView, error) {
	// Check if user is member
	isMember, err := s.groupRepo.IsMember(ctx, groupID, userID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to check membership", err)
	}
	if !isMember {
		return nil, apperrors.NotGroupMember()
	}

	// Validate pagination
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	// Получаем все сообщения
	messages, err := s.messageRepo.GetConversationMessages(ctx, groupID, userID, limit, offset)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to get messages", err)
	}

	// ВАЖНО: Фильтруем сообщения по last_cleared_at
	// Показываем только сообщения после того времени, когда пользователь скрыл группу
	lastClearedAt, _ := s.groupRepo.GetLastClearedAt(ctx, groupID, userID)

	var processedMessages []*domain.MessageView
	if lastClearedAt != nil {
		for _, msg := range messages {
			if msg.CreatedAt.After(*lastClearedAt) {
				processedMessages = append(processedMessages, msg)
			}
		}
	} else {
		processedMessages = messages
	}

	currentDeviceID = strings.TrimSpace(currentDeviceID)
	if currentDeviceID != "" {
		equivalentDeviceIDs, aliasErr := s.messageRepo.GetEquivalentDeviceIDs(ctx, userID, currentDeviceID)
		if aliasErr == nil {
			aliasEncryptedKeysForDevice(processedMessages, currentDeviceID, equivalentDeviceIDs)
		} else {
			s.logger.Warn("failed to resolve equivalent device ids for group history access",
				zap.Int64("user_id", userID),
				zap.String("device_id", currentDeviceID),
				zap.Error(aliasErr),
			)
		}
	}

	// Enrich with reactions
	if len(processedMessages) > 0 {
		msgIDs := make([]uuid.UUID, len(processedMessages))
		for i, m := range processedMessages {
			msgIDs[i] = m.ID
		}

		reactionsMap, err := s.reactionRepo.GetReactionsByMessageIDs(ctx, msgIDs)
		if err == nil {
			for _, m := range processedMessages {
				if reacts, ok := reactionsMap[m.ID]; ok {
					m.Reactions = s.buildReactionSummaries(reacts, userID)
				}
			}
		} else {
			s.logger.Warn("Failed to fetch reactions", zap.Error(err))
		}
	}

	return processedMessages, nil
}

// GetSharedMedia returns paginated image/video attachments for a group.
func (s *GroupService) GetSharedMedia(ctx context.Context, groupID uuid.UUID, userID int64, limit, offset int) (*domain.SharedMediaResponse, error) {
	isMember, err := s.groupRepo.IsMember(ctx, groupID, userID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to check membership", err)
	}
	if !isMember {
		return nil, apperrors.NotGroupMember()
	}

	if limit <= 0 || limit > 100 {
		limit = 30
	}
	if offset < 0 {
		offset = 0
	}

	lastClearedAt, err := s.groupRepo.GetLastClearedAt(ctx, groupID, userID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to get group clear state", err)
	}

	items, total, err := s.messageRepo.GetConversationSharedMedia(ctx, groupID, userID, limit, offset, lastClearedAt)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to get shared media", err)
	}

	return &domain.SharedMediaResponse{
		Items:   items,
		Total:   total,
		HasMore: offset+len(items) < total,
	}, nil
}

func (s *GroupService) buildReactionSummaries(reactions []domain.MessageReaction, currentUserID int64) []domain.ReactionSummary {
	summaryMap := make(map[string]*domain.ReactionSummary)

	for _, r := range reactions {
		if _, exists := summaryMap[r.Reaction]; !exists {
			summaryMap[r.Reaction] = &domain.ReactionSummary{
				Reaction: r.Reaction,
				Count:    0,
				Reacted:  false,
			}
		}
		summary := summaryMap[r.Reaction]
		summary.Count++
		if r.UserID == currentUserID {
			summary.Reacted = true
		}
		if len(summary.Users) < 50 && r.UserFullName != "" {
			summary.Users = append(summary.Users, domain.ReactionUser{
				ID:       r.UserID,
				FullName: r.UserFullName,
				Avatar:   r.UserAvatar,
			})
		}
	}

	result := make([]domain.ReactionSummary, 0, len(summaryMap))
	for _, s := range summaryMap {
		result = append(result, *s)
	}
	return result
}

// HideGroup скрывает группу для пользователя
// Любой участник может скрыть группу у себя, это не повлияет на других
func (s *GroupService) HideGroup(ctx context.Context, groupID uuid.UUID, userID int64) error {
	// Verify user is member
	isMember, err := s.groupRepo.IsMember(ctx, groupID, userID)
	if err != nil {
		return apperrors.InternalWithErr("failed to verify member", err)
	}
	if !isMember {
		return apperrors.NotGroupMember()
	}

	// Hide group for this user only
	if err := s.groupRepo.HideGroup(ctx, groupID, userID); err != nil {
		return apperrors.InternalWithErr("failed to hide group", err)
	}

	// Notify all devices of this user that the group chat was hidden/cleared
	if s.hub.IsUserOnline(userID) {
		s.hub.SendChatHiddenNotification(userID, groupID, "group")
	}

	// Unpin group if it was pinned
	if err := s.pinnedRepo.UnpinChat(ctx, userID, groupID, "group"); err != nil {
		s.logger.Warn("Failed to unpin hidden group",
			zap.String("group_id", groupID.String()),
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
	}

	s.logger.Info("Group hidden by user",
		zap.String("group_id", groupID.String()),
		zap.Int64("user_id", userID),
	)

	return nil
}

// UnhideGroup восстанавливает скрытую группу для пользователя
func (s *GroupService) UnhideGroup(ctx context.Context, groupID uuid.UUID, userID int64) error {
	// Verify user is member
	isMember, err := s.groupRepo.IsMember(ctx, groupID, userID)
	if err != nil {
		return apperrors.InternalWithErr("failed to verify member", err)
	}
	if !isMember {
		return apperrors.NotGroupMember()
	}

	// Unhide group
	if err := s.groupRepo.UnhideGroup(ctx, groupID, userID); err != nil {
		return apperrors.InternalWithErr("failed to unhide group", err)
	}

	s.logger.Info("Group unhidden by user",
		zap.String("group_id", groupID.String()),
		zap.Int64("user_id", userID),
	)

	return nil
}

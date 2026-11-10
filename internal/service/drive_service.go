package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"github.com/xuri/excelize/v2"
	"go.uber.org/zap"
)

type DriveService struct {
	repo                   *repository.DriveRepository
	recentRepo             *repository.RecentRepository
	fileSvc                *FileService
	publicURL              string
	notifService           NotificationSender
	gamification           *GamificationService // новое поле
	driveSearch            *DriveSearchService
	logger                 *zap.Logger
	pendingFolderIndexSync sync.Map
}

type ExternalDriveImportRequest struct {
	FolderID           *uuid.UUID
	AttachmentURL      string
	AttachmentName     string
	AttachmentMimeType string
	AttachmentContent  []byte
	SourceSystemCode   string
	SourceSystemName   string
	ExternalMessageID  string
}

func NewDriveService(
	repo *repository.DriveRepository,
	recentRepo *repository.RecentRepository,
	fileSvc *FileService,
	publicURL string,
	notifService NotificationSender,
	gamification *GamificationService,
	logger *zap.Logger,
) *DriveService {
	return &DriveService{
		repo:         repo,
		recentRepo:   recentRepo,
		fileSvc:      fileSvc,
		publicURL:    publicURL,
		notifService: notifService,
		gamification: gamification,
		logger:       logger.With(zap.String("service", "drive")),
	}
}

func (s *DriveService) SetDriveSearchService(driveSearch *DriveSearchService) {
	s.driveSearch = driveSearch
}

func (s *DriveService) syncDriveFileIndex(fileID uuid.UUID) {
	if s.driveSearch != nil {
		s.driveSearch.IndexFileAsync(fileID)
	}
}

func (s *DriveService) deleteDriveFileIndex(fileID uuid.UUID) {
	if s.driveSearch != nil {
		s.driveSearch.DeleteFileAsync(fileID)
	}
}

func (s *DriveService) syncDriveFolderIndex(ctx context.Context, folderID uuid.UUID) {
	if s.driveSearch == nil {
		return
	}
	fileIDs, err := s.repo.ListFileIDsInFolderTree(ctx, folderID)
	if err != nil {
		s.logger.Warn("failed to list folder files for drive search sync",
			zap.String("folder_id", folderID.String()),
			zap.Error(err),
		)
		return
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, 10)

	for _, fileID := range fileIDs {
		wg.Add(1)
		sem <- struct{}{}
		go func(fid uuid.UUID) {
			defer wg.Done()
			defer func() { <-sem }()

			asyncCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			if err := s.driveSearch.UpdateAllowedUsers(asyncCtx, fid); err != nil {
				s.logger.Warn("failed to update allowed users in elasticsearch",
					zap.String("file_id", fid.String()),
					zap.Error(err),
				)
			}
		}(fileID)
	}
	wg.Wait()
}

func (s *DriveService) queueDriveFolderIndexSync(folderID uuid.UUID) {
	if s.driveSearch == nil {
		return
	}
	if _, loaded := s.pendingFolderIndexSync.LoadOrStore(folderID, struct{}{}); loaded {
		return
	}

	go func() {
		defer s.pendingFolderIndexSync.Delete(folderID)

		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()

		s.syncDriveFolderIndex(ctx, folderID)
	}()
}

func (s *DriveService) deleteDriveFolderIndex(ctx context.Context, folderID uuid.UUID) {
	if s.driveSearch == nil {
		return
	}
	fileIDs, err := s.repo.ListFileIDsInFolderTree(ctx, folderID)
	if err != nil {
		s.logger.Warn("failed to list folder files for drive search delete",
			zap.String("folder_id", folderID.String()),
			zap.Error(err),
		)
		return
	}
	s.driveSearch.DeleteFilesBulkAsync(fileIDs)
}

// recordActivity асинхронно сохраняет факт доступа к ресурсу.
// Используем отдельный контекст с таймаутом, чтобы не зависеть от отмены запроса клиента.
func (s *DriveService) recordActivity(userID int64, t domain.ResourceType, id uuid.UUID) {
	if userID == 0 {
		return
	}
	go func() {
		// Создаем background context, так как родительский ctx может быть отменен (request finished)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		if err := s.recentRepo.Touch(ctx, userID, t, id); err != nil {
			s.logger.Error("failed to record recent activity",
				zap.Error(err),
				zap.Int64("user_id", userID),
				zap.String("resource_id", id.String()),
			)
		}
	}()
}

func joinDrivePath(parentPath, name string) string {
	name = strings.TrimSpace(name)
	parentPath = strings.TrimSpace(parentPath)
	switch {
	case parentPath == "":
		return name
	case name == "":
		return parentPath
	default:
		return parentPath + " / " + name
	}
}

func cloneUUIDPtr(id *uuid.UUID) *uuid.UUID {
	if id == nil {
		return nil
	}
	cloned := *id
	return &cloned
}

func rawJSONMap(payload map[string]any) json.RawMessage {
	if len(payload) == 0 {
		return json.RawMessage(`{}`)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return data
}

func (s *DriveService) buildFolderPathByParent(ctx context.Context, parentID *uuid.UUID, name string) (string, error) {
	if parentID == nil {
		return strings.TrimSpace(name), nil
	}

	parentPath, err := s.buildFolderPath(ctx, *parentID)
	if err != nil {
		return "", err
	}
	return joinDrivePath(parentPath, name), nil
}

func (s *DriveService) buildFolderPath(ctx context.Context, folderID uuid.UUID) (string, error) {
	folder, err := s.repo.GetFolderByID(ctx, folderID)
	if err != nil {
		return "", err
	}
	if folder == nil {
		return "", fmt.Errorf("folder not found")
	}
	return s.buildFolderPathByParent(ctx, folder.ParentID, folder.Name)
}

func (s *DriveService) buildFilePath(ctx context.Context, folderID *uuid.UUID, name string) (string, error) {
	if folderID == nil {
		return strings.TrimSpace(name), nil
	}

	folderPath, err := s.buildFolderPath(ctx, *folderID)
	if err != nil {
		return "", err
	}
	return joinDrivePath(folderPath, name), nil
}

func (s *DriveService) logDriveActivity(ctx context.Context, activity *domain.DriveActivity) {
	if activity == nil {
		return
	}
	if activity.ID == uuid.Nil {
		activity.ID = uuid.New()
	}
	if activity.CreatedAt.IsZero() {
		activity.CreatedAt = time.Now()
	}
	if err := s.repo.LogDriveActivity(ctx, activity); err != nil {
		s.logger.Warn("failed to log drive activity",
			zap.String("action", string(activity.Action)),
			zap.String("resource_type", string(activity.ResourceType)),
			zap.String("resource_id", activity.ResourceID.String()),
			zap.Error(err),
		)
	}
}

func roleRank(r domain.PermissionRole) int {
	switch r {
	case domain.RoleOwner:
		return 3
	case domain.RoleEditor:
		return 2
	case domain.RoleViewer:
		return 1
	default:
		return 0
	}
}

func clonePermissionRole(role domain.PermissionRole) *domain.PermissionRole {
	cloned := role
	return &cloned
}

func strongerRole(current *domain.PermissionRole, candidate domain.PermissionRole) *domain.PermissionRole {
	if current == nil || roleRank(candidate) > roleRank(*current) {
		return clonePermissionRole(candidate)
	}
	return current
}

func normalizeFileRoleForFolderWorkspace(role domain.PermissionRole) domain.PermissionRole {
	if roleRank(role) >= roleRank(domain.RoleEditor) {
		return domain.RoleEditor
	}
	return domain.RoleViewer
}

func (s *DriveService) getEffectiveFolderRole(ctx context.Context, userID int64, folder *domain.Folder) (*domain.PermissionRole, error) {
	if folder == nil || userID == 0 {
		return nil, nil
	}

	var best *domain.PermissionRole
	current := folder
	visited := make(map[uuid.UUID]struct{})

	for current != nil {
		if _, seen := visited[current.ID]; seen {
			break
		}
		visited[current.ID] = struct{}{}

		if current.IsPublic {
			best = strongerRole(best, domain.RoleViewer)
		}
		if current.OwnerID == userID {
			return clonePermissionRole(domain.RoleOwner), nil
		}

		role, err := s.repo.GetPermissionRole(ctx, domain.ResourceFolder, current.ID, userID)
		if err != nil {
			return nil, err
		}
		if role != nil {
			best = strongerRole(best, *role)
		}

		if current.ParentID == nil {
			break
		}

		parent, err := s.repo.GetFolderByID(ctx, *current.ParentID)
		if err != nil || parent == nil || parent.DeletedAt != nil {
			break
		}
		current = parent
	}

	return best, nil
}

func (s *DriveService) getDirectFileWorkspaceRoleInFolder(ctx context.Context, userID int64, folderID uuid.UUID) (*domain.PermissionRole, error) {
	files, err := s.repo.ListFilesByFolder(ctx, &folderID)
	if err != nil {
		return nil, err
	}

	var best *domain.PermissionRole
	for _, file := range files {
		if file.OwnerID == userID {
			best = strongerRole(best, domain.RoleEditor)
			continue
		}

		role, err := s.repo.GetPermissionRole(ctx, domain.ResourceFile, file.ID, userID)
		if err != nil {
			return nil, err
		}
		if role == nil {
			continue
		}

		best = strongerRole(best, normalizeFileRoleForFolderWorkspace(*role))
	}

	return best, nil
}

func (s *DriveService) getEffectiveFolderWorkspaceRole(ctx context.Context, userID int64, folder *domain.Folder) (*domain.PermissionRole, error) {
	// Access flows down from ancestors, never up from an individually shared file.
	return s.getEffectiveFolderRole(ctx, userID, folder)
}

func (s *DriveService) getEffectiveFileRole(ctx context.Context, userID int64, file *domain.DriveFile) (*domain.PermissionRole, error) {
	if file == nil || userID == 0 {
		return nil, nil
	}
	if file.OwnerID == userID {
		return clonePermissionRole(domain.RoleOwner), nil
	}

	var best *domain.PermissionRole
	if file.IsPublic {
		best = clonePermissionRole(domain.RoleViewer)
	}

	role, err := s.repo.GetPermissionRole(ctx, domain.ResourceFile, file.ID, userID)
	if err != nil {
		return nil, err
	}
	if role != nil {
		best = strongerRole(best, *role)
	}

	if file.FolderID != nil {
		folder, err := s.repo.GetFolderByID(ctx, *file.FolderID)
		if err == nil && folder != nil && folder.DeletedAt == nil {
			folderRole, err := s.getEffectiveFolderRole(ctx, userID, folder)
			if err != nil {
				return nil, err
			}
			if folderRole != nil {
				best = strongerRole(best, *folderRole)
			}
		}
	}

	return best, nil
}

func (s *DriveService) folderAccessDeniedError(ctx context.Context, userID int64, folder *domain.Folder, need domain.PermissionRole) *apperrors.AppError {
	err := apperrors.Forbidden("no access to folder").WithDetails(map[string]any{
		"resource_type": string(domain.ResourceFolder),
		"resource_id":   folder.ID.String(),
		"required_role": string(need),
	})

	fileRole, checkErr := s.getDirectFileWorkspaceRoleInFolder(ctx, userID, folder.ID)
	if checkErr != nil {
		s.logger.Warn("failed to inspect file-level access for folder denial",
			zap.Error(checkErr),
			zap.Int64("user_id", userID),
			zap.String("folder_id", folder.ID.String()),
		)
		return err
	}
	if fileRole == nil {
		return err
	}

	if roleRank(*fileRole) < roleRank(need) {
		return apperrors.Forbidden("no access to folder: your access to files inside it is not enough for this action").WithDetails(map[string]any{
			"resource_type":     string(domain.ResourceFolder),
			"resource_id":       folder.ID.String(),
			"required_role":     string(need),
			"current_file_role": string(*fileRole),
			"reason":            "insufficient_file_access_for_folder_action",
			"hint":              fmt.Sprintf("you have %s access to files inside this folder, but %s is required for this action", *fileRole, need),
		})
	}

	return apperrors.Forbidden("no access to folder: you only have access to individual files inside it").WithDetails(map[string]any{
		"resource_type":     string(domain.ResourceFolder),
		"resource_id":       folder.ID.String(),
		"required_role":     string(need),
		"current_file_role": string(*fileRole),
		"reason":            "file_access_without_folder_access",
		"hint":              "ask the folder owner to grant access to the folder to browse it or manage its sharing settings directly",
	})
}

func (s *DriveService) canAccessFolder(ctx context.Context, userID int64, folderID uuid.UUID, need domain.PermissionRole) (*domain.Folder, error) {
	f, err := s.repo.GetFolderByID(ctx, folderID)
	if err != nil || f == nil || f.DeletedAt != nil {
		return nil, apperrors.NotFound("folder not found")
	}
	role, err := s.getEffectiveFolderRole(ctx, userID, f)
	if err != nil {
		return nil, apperrors.Internal("permission lookup failed")
	}
	if role == nil || roleRank(*role) < roleRank(need) {
		if f.IsPublic && roleRank(domain.RoleViewer) >= roleRank(need) {
			return f, nil
		}
		return nil, s.folderAccessDeniedError(ctx, userID, f, need)
	}
	return f, nil
}

func (s *DriveService) canWorkInFolder(ctx context.Context, userID int64, folderID uuid.UUID, need domain.PermissionRole) (*domain.Folder, error) {
	f, err := s.repo.GetFolderByID(ctx, folderID)
	if err != nil || f == nil || f.DeletedAt != nil {
		return nil, apperrors.NotFound("folder not found")
	}

	role, err := s.getEffectiveFolderWorkspaceRole(ctx, userID, f)
	if err != nil {
		return nil, apperrors.Internal("permission lookup failed")
	}
	if role == nil || roleRank(*role) < roleRank(need) {
		return nil, s.folderAccessDeniedError(ctx, userID, f, need)
	}

	return f, nil
}

func (s *DriveService) canAccessFile(ctx context.Context, userID int64, fileID uuid.UUID, need domain.PermissionRole) (*domain.DriveFile, error) {
	f, err := s.repo.GetFileByID(ctx, fileID)
	if err != nil || f == nil || f.DeletedAt != nil {
		return nil, apperrors.NotFound("file not found")
	}
	if userID == 0 {
		if f.IsPublic && roleRank(domain.RoleViewer) >= roleRank(need) {
			return f, nil
		}
		return nil, apperrors.Unauthorized("unauthorized")
	}
	role, err := s.getEffectiveFileRole(ctx, userID, f)
	if err != nil {
		return nil, apperrors.Internal("permission lookup failed")
	}
	if role == nil || roleRank(*role) < roleRank(need) {
		if f.IsPublic && roleRank(domain.RoleViewer) >= roleRank(need) {
			return f, nil
		}
		return nil, apperrors.Forbidden("no access to file").WithDetails(map[string]any{
			"resource_type": string(domain.ResourceFile),
			"resource_id":   f.ID.String(),
			"required_role": string(need),
		})
	}

	return f, nil
}

//nolint:unused
func (s *DriveService) ensureDeleteApprovedByFolderOwner(ctx context.Context, userID int64, parentFolderID *uuid.UUID) error {
	if parentFolderID == nil {
		return nil
	}

	parentFolder, err := s.repo.GetFolderByID(ctx, *parentFolderID)
	if err != nil || parentFolder == nil || parentFolder.DeletedAt != nil {
		return apperrors.NotFound("parent folder not found")
	}
	if parentFolder.OwnerID == userID {
		return nil
	}

	return apperrors.Forbidden("delete inside shared folder requires owner approval").
		WithUserMessage("Удаление внутри этой папки доступно только владельцу папки. Согласуйте удаление с владельцем.")
}

func (s *DriveService) shouldRequireDeleteApproval(ctx context.Context, userID int64, directOwnerID int64, parentFolderID *uuid.UUID) (bool, int64, error) {
	if directOwnerID != 0 && directOwnerID != userID {
		return true, directOwnerID, nil
	}

	if parentFolderID == nil {
		return false, 0, nil
	}

	parentFolder, err := s.repo.GetFolderByID(ctx, *parentFolderID)
	if err != nil || parentFolder == nil || parentFolder.DeletedAt != nil {
		return false, 0, apperrors.NotFound("parent folder not found")
	}

	if parentFolder.OwnerID == userID {
		return false, parentFolder.OwnerID, nil
	}

	return true, parentFolder.OwnerID, nil
}

func (s *DriveService) notifyDriveDeleteApprovalRequested(ctx context.Context, req *domain.DriveDeleteRequest) error {
	if s.notifService == nil || req == nil {
		return nil
	}

	resourceLabel := "элемента"
	if req.ResourceType == domain.ResourceFolder {
		resourceLabel = "папки"
	} else if req.ResourceType == domain.ResourceFile {
		resourceLabel = "файла"
	}

	requesterName := ""
	if name, err := s.repo.GetUserNameByID(ctx, req.RequestedBy); err == nil && name != "" {
		requesterName = " " + name
	}

	notifReq := domain.CreateNotificationRequest{
		UserID:          req.OwnerID,
		Type:            "drive_delete_approval_requested",
		TitleTemplate:   &domain.NotificationTemplate{Source: "Запрос на удаление в диске", Args: []any{}},
		Title:           "Запрос на удаление в диске",
		ContentTemplate: &domain.NotificationTemplate{Source: "Пользователь%s запросил удаление %s \"%s\". Подтвердите или отклоните запрос.", Args: []any{requesterName, domain.NotificationLabel(resourceLabel), req.ResourceName}},
		Content: fmt.Sprintf(
			"Пользователь%s запросил удаление %s \"%s\". Подтвердите или отклоните запрос.",
			requesterName,
			resourceLabel,
			req.ResourceName,
		),
		Metadata: map[string]string{
			"kind":                        "drive_delete_approval",
			"delete_request_id":           req.ID.String(),
			"drive_delete_request_status": string(domain.DriveDeleteRequestPending),
			"resource_type":               string(req.ResourceType),
			"resource_id":                 req.ResourceID.String(),
			"resource_name":               req.ResourceName,
			"requested_by":                fmt.Sprintf("%d", req.RequestedBy),
			"owner_id":                    fmt.Sprintf("%d", req.OwnerID),
			"recursive":                   fmt.Sprintf("%t", req.Recursive),
		},
	}
	if req.ParentFolderID != nil {
		notifReq.Metadata["parent_folder_id"] = req.ParentFolderID.String()
	}

	if err := s.notifService.CreateAndSendNotification(ctx, notifReq); err != nil {
		return err
	}
	return nil
}

func (s *DriveService) notifyDriveDeleteRequestResolved(ctx context.Context, req *domain.DriveDeleteRequest, approved bool, rejectionReason string) {
	if s.notifService == nil || req == nil {
		return
	}

	resourceLabel := "Элемент"
	if req.ResourceType == domain.ResourceFolder {
		resourceLabel = "Папка"
	} else if req.ResourceType == domain.ResourceFile {
		resourceLabel = "Файл"
	}

	title := "Удаление отклонено"
	content := fmt.Sprintf("%s \"%s\" не был удалён: владелец отклонил запрос.", resourceLabel, req.ResourceName)
	status := string(domain.DriveDeleteRequestRejected)
	if approved {
		title = "Удаление подтверждено"
		content = fmt.Sprintf("%s \"%s\" был удалён после согласования владельцем.", resourceLabel, req.ResourceName)
		status = "approved"
	} else if strings.TrimSpace(rejectionReason) != "" {
		content = fmt.Sprintf("%s \"%s\" не был удалён: %s", resourceLabel, req.ResourceName, strings.TrimSpace(rejectionReason))
	}

	contentTemplate := &domain.NotificationTemplate{Source: "%s \"%s\" не был удалён: владелец отклонил запрос.", Args: []any{domain.NotificationLabel(resourceLabel), req.ResourceName}}
	if approved {
		contentTemplate.Source = "%s \"%s\" был удалён после согласования владельцем."
	} else if strings.TrimSpace(rejectionReason) != "" {
		contentTemplate.Source = "%s \"%s\" не был удалён: %s"
		contentTemplate.Args = append(contentTemplate.Args, strings.TrimSpace(rejectionReason))
	}
	notifReq := domain.CreateNotificationRequest{
		TitleTemplate: &domain.NotificationTemplate{Source: title}, ContentTemplate: contentTemplate,
		UserID:  req.RequestedBy,
		Type:    "drive_delete_approval_result",
		Title:   title,
		Content: content,
		Metadata: map[string]string{
			"kind":                        "drive_delete_request_result",
			"delete_request_id":           req.ID.String(),
			"drive_delete_request_status": status,
			"resource_type":               string(req.ResourceType),
			"resource_id":                 req.ResourceID.String(),
			"resource_name":               req.ResourceName,
			"owner_id":                    fmt.Sprintf("%d", req.OwnerID),
			"requested_by":                fmt.Sprintf("%d", req.RequestedBy),
		},
	}
	if strings.TrimSpace(rejectionReason) != "" {
		notifReq.Metadata["rejection_reason"] = strings.TrimSpace(rejectionReason)
	}

	if err := s.notifService.CreateAndSendNotification(ctx, notifReq); err != nil {
		s.logger.Warn("failed to send drive delete resolution notification",
			zap.String("delete_request_id", req.ID.String()),
			zap.Int64("requested_by", req.RequestedBy),
			zap.Error(err),
		)
	}
}

func (s *DriveService) createOrReuseDeleteRequest(
	ctx context.Context,
	userID int64,
	resourceType domain.ResourceType,
	resourceID uuid.UUID,
	resourceName string,
	parentFolderID *uuid.UUID,
	ownerID int64,
	recursive bool,
) (*domain.DriveDeleteOperationResult, error) {
	existing, err := s.repo.FindPendingDeleteRequest(ctx, resourceType, resourceID, userID)
	if err != nil {
		return nil, apperrors.InternalWithErr("find pending delete request failed", err)
	}
	if existing != nil {
		if err := s.notifyDriveDeleteApprovalRequested(ctx, existing); err != nil {
			s.logger.Warn("failed to re-send drive delete approval notification",
				zap.String("delete_request_id", existing.ID.String()),
				zap.Int64("owner_id", existing.OwnerID),
				zap.Error(err),
			)
		}
		return &domain.DriveDeleteOperationResult{
			Status:          "pending_approval",
			Message:         "Запрос на удаление уже отправлен владельцу папки.",
			DeleteRequestID: &existing.ID,
		}, nil
	}

	req := &domain.DriveDeleteRequest{
		ID:             uuid.New(),
		ResourceType:   resourceType,
		ResourceID:     resourceID,
		ResourceName:   resourceName,
		ParentFolderID: parentFolderID,
		OwnerID:        ownerID,
		RequestedBy:    userID,
		Recursive:      recursive,
		Status:         domain.DriveDeleteRequestPending,
		CreatedAt:      time.Now(),
	}
	if err := s.repo.CreateDeleteRequest(ctx, req); err != nil {
		return nil, apperrors.InternalWithErr("create drive delete request failed", err)
	}

	if err := s.notifyDriveDeleteApprovalRequested(ctx, req); err != nil {
		if cleanupErr := s.repo.DeleteDeleteRequest(ctx, req.ID); cleanupErr != nil {
			s.logger.Error("failed to rollback drive delete request after notification error",
				zap.String("delete_request_id", req.ID.String()),
				zap.Error(cleanupErr),
			)
		}
		return nil, apperrors.InternalWithErr("send drive delete approval notification failed", err).
			WithUserMessage("Не удалось отправить запрос владельцу папки. Повторите попытку.")
	}

	return &domain.DriveDeleteOperationResult{
		Status:          "pending_approval",
		Message:         "Запрос на удаление отправлен владельцу папки.",
		DeleteRequestID: &req.ID,
	}, nil
}

func (s *DriveService) executeDeleteFolder(ctx context.Context, folderID uuid.UUID, recursive bool) error {
	if !recursive {
		hasChildren, err := s.repo.FolderHasChildren(ctx, folderID)
		if err != nil {
			return apperrors.Internal("check folder children failed")
		}
		if hasChildren {
			return apperrors.BadRequest("folder is not empty (use ?recursive=true)")
		}
		if err := s.repo.SoftDeleteFolder(ctx, folderID); err != nil {
			if err.Error() == "folder not found" {
				return apperrors.NotFound("folder not found")
			}
			return apperrors.Internal("delete folder failed")
		}
		s.deleteDriveFolderIndex(ctx, folderID)
		return nil
	}

	s.deleteDriveFolderIndex(ctx, folderID)
	if err := s.repo.SoftDeleteFolderTree(ctx, folderID); err != nil {
		if err.Error() == "folder not found" {
			return apperrors.NotFound("folder not found")
		}
		return apperrors.Internal("recursive delete folder failed")
	}
	return nil
}

func (s *DriveService) executeDeleteFile(ctx context.Context, fileID uuid.UUID) error {
	if err := s.repo.SoftDeleteFile(ctx, fileID); err != nil {
		if err.Error() == "file not found" {
			return apperrors.NotFound("file not found")
		}
		return apperrors.Internal("delete file failed")
	}
	s.deleteDriveFileIndex(fileID)
	return nil
}

func (s *DriveService) CreateFolder(ctx context.Context, userID int64, parentID *uuid.UUID, name string) (*domain.Folder, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, apperrors.BadRequest("name is required")
	}
	if parentID != nil {
		if _, err := s.canWorkInFolder(ctx, userID, *parentID, domain.RoleEditor); err != nil {
			return nil, err
		}
	}
	if exists, err := s.repo.FolderNameExistsInParent(ctx, userID, parentID, name, nil); err != nil {
		return nil, apperrors.InternalWithErr("folder duplicate check failed", err)
	} else if exists {
		return nil, apperrors.Conflict("folder with the same name already exists in the target location").
			WithUserMessage("В этой папке уже есть папка с таким названием.")
	}

	now := time.Now()
	f := &domain.Folder{
		ID:        uuid.New(),
		OwnerID:   userID,
		ParentID:  parentID,
		Name:      name,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.repo.CreateFolder(ctx, f); err != nil {
		return nil, apperrors.Internal("create folder failed")
	}
	if resourcePath, err := s.buildFolderPathByParent(ctx, parentID, f.Name); err == nil {
		s.logDriveActivity(ctx, &domain.DriveActivity{
			Action:       domain.DriveActivityActionCreated,
			ResourceType: domain.ResourceFolder,
			ResourceID:   f.ID,
			ResourceName: f.Name,
			OwnerID:      f.OwnerID,
			ActorID:      userID,
			FolderID:     cloneUUIDPtr(parentID),
			ToFolderID:   cloneUUIDPtr(parentID),
			ResourcePath: resourcePath,
			ToPath:       resourcePath,
		})
	}

	// Гамификация: создание папки
	if s.gamification != nil {
		go func() { _ = s.gamification.TrackActivity(context.Background(), userID, domain.ActionFolderCreated) }()
	}

	// При создании папки логично добавить ее в недавние
	s.recordActivity(userID, domain.ResourceFolder, f.ID)

	return f, nil
}

type FolderBreadcrumb struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

func (s *DriveService) GetFolderPath(ctx context.Context, userID int64, folderID uuid.UUID) ([]FolderBreadcrumb, error) {
	var path []FolderBreadcrumb
	currID := folderID
	for i := 0; i < 50; i++ {
		f, err := s.canAccessFolder(ctx, userID, currID, domain.RoleViewer)
		if err != nil {
			break
		}
		path = append([]FolderBreadcrumb{{
			ID:   f.ID,
			Name: f.Name,
		}}, path...)
		if f.ParentID == nil {
			break
		}
		currID = *f.ParentID
	}
	return path, nil
}

func (s *DriveService) ListFolderChildren(ctx context.Context, userID int64, folderID *uuid.UUID) ([]domain.DriveItem, error) {
	if folderID != nil {
		// Проверяем доступ
		if _, err := s.canWorkInFolder(ctx, userID, *folderID, domain.RoleViewer); err != nil {
			return nil, err
		}

		// ✅ Фиксируем активность: пользователь открыл папку
		s.recordActivity(userID, domain.ResourceFolder, *folderID)
	}

	folders, err := s.repo.ListFoldersByParent(ctx, folderID)
	if err != nil {
		return nil, apperrors.Internal("list folders failed")
	}
	files, err := s.repo.ListFilesByFolder(ctx, folderID)
	if err != nil {
		return nil, apperrors.Internal("list files failed")
	}

	// root: показываем только свои
	if folderID == nil {
		var ownFolders []*domain.Folder
		for _, f := range folders {
			if f.OwnerID == userID {
				ownFolders = append(ownFolders, f)
			}
		}
		folders = ownFolders

		var ownFiles []*domain.DriveFile
		for _, f := range files {
			if f.OwnerID == userID {
				ownFiles = append(ownFiles, f)
			}
		}
		files = ownFiles
	}

	items := make([]domain.DriveItem, 0, len(folders)+len(files))
	for _, f := range folders {
		items = append(items, domain.DriveItem{Type: domain.ResourceFolder, Folder: f})
	}
	for _, f := range files {
		items = append(items, domain.DriveItem{Type: domain.ResourceFile, File: f})
	}
	return items, nil
}

func (s *DriveService) UpdateFolder(ctx context.Context, userID int64, folderID uuid.UUID, name *string, parentID *uuid.UUID) error {
	folder, err := s.canAccessFolder(ctx, userID, folderID, domain.RoleEditor)
	if err != nil {
		return err
	}
	if err := s.validateFolderDestination(ctx, folderID, parentID); err != nil {
		return err
	}
	originalName := folder.Name
	originalParentID := cloneUUIDPtr(folder.ParentID)
	originalPath, _ := s.buildFolderPathByParent(ctx, folder.ParentID, folder.Name)
	if parentID != nil {
		if _, err := s.canAccessFolder(ctx, userID, *parentID, domain.RoleEditor); err != nil {
			return err
		}
	}

	targetParentID := folder.ParentID
	if parentID != nil {
		targetParentID = parentID
	}
	targetName := folder.Name
	if name != nil {
		targetName = strings.TrimSpace(*name)
		if err := validateDriveName(targetName); err != nil {
			return err
		}
		name = &targetName
	}
	if exists, err := s.repo.FolderNameExistsInParent(ctx, folder.OwnerID, targetParentID, targetName, &folderID); err != nil {
		return apperrors.InternalWithErr("folder duplicate check failed", err)
	} else if exists {
		return apperrors.Conflict("folder with the same name already exists in the target location").
			WithUserMessage("В этой папке уже есть папка с таким названием.")
	}

	if err := s.repo.UpdateFolder(ctx, folderID, name, parentID); err != nil {
		if errors.Is(err, repository.ErrFolderCycle) {
			return apperrors.BadRequest(err.Error())
		}
		if err.Error() == "folder not found" {
			return apperrors.NotFound("folder not found")
		}
		return apperrors.Internal("update folder failed")
	}
	updatedPath, _ := s.buildFolderPathByParent(ctx, targetParentID, targetName)
	if originalName != targetName {
		s.logDriveActivity(ctx, &domain.DriveActivity{
			Action:       domain.DriveActivityActionRenamed,
			ResourceType: domain.ResourceFolder,
			ResourceID:   folderID,
			ResourceName: targetName,
			OwnerID:      folder.OwnerID,
			ActorID:      userID,
			FolderID:     cloneUUIDPtr(targetParentID),
			FromFolderID: cloneUUIDPtr(originalParentID),
			ToFolderID:   cloneUUIDPtr(targetParentID),
			ResourcePath: updatedPath,
			FromPath:     originalPath,
			ToPath:       updatedPath,
			Metadata: rawJSONMap(map[string]any{
				"old_name": originalName,
				"new_name": targetName,
			}),
		})
	}
	parentChanged := (originalParentID == nil) != (targetParentID == nil)
	if !parentChanged && originalParentID != nil && targetParentID != nil {
		parentChanged = *originalParentID != *targetParentID
	}
	if parentChanged {
		s.logDriveActivity(ctx, &domain.DriveActivity{
			Action:       domain.DriveActivityActionMoved,
			ResourceType: domain.ResourceFolder,
			ResourceID:   folderID,
			ResourceName: targetName,
			OwnerID:      folder.OwnerID,
			ActorID:      userID,
			FolderID:     cloneUUIDPtr(targetParentID),
			FromFolderID: cloneUUIDPtr(originalParentID),
			ToFolderID:   cloneUUIDPtr(targetParentID),
			ResourcePath: updatedPath,
			FromPath:     originalPath,
			ToPath:       updatedPath,
		})
	}

	// При переименовании тоже обновляем "недавние"
	s.recordActivity(userID, domain.ResourceFolder, folderID)

	return nil
}

func (s *DriveService) DeleteFolder(ctx context.Context, userID int64, folderID uuid.UUID, recursive bool) (*domain.DriveDeleteOperationResult, error) {
	folder, err := s.canAccessFolder(ctx, userID, folderID, domain.RoleEditor)
	if err != nil {
		return nil, err
	}
	folderPath, _ := s.buildFolderPathByParent(ctx, folder.ParentID, folder.Name)
	requiresApproval, ownerID, err := s.shouldRequireDeleteApproval(ctx, userID, folder.OwnerID, folder.ParentID)
	if err != nil {
		return nil, err
	}
	if requiresApproval {
		return s.createOrReuseDeleteRequest(ctx, userID, domain.ResourceFolder, folder.ID, folder.Name, folder.ParentID, ownerID, recursive)
	}

	if err := s.executeDeleteFolder(ctx, folderID, recursive); err != nil {
		return nil, err
	}
	s.logDriveActivity(ctx, &domain.DriveActivity{
		Action:       domain.DriveActivityActionDeleted,
		ResourceType: domain.ResourceFolder,
		ResourceID:   folderID,
		ResourceName: folder.Name,
		OwnerID:      folder.OwnerID,
		ActorID:      userID,
		FolderID:     cloneUUIDPtr(folder.ParentID),
		FromFolderID: cloneUUIDPtr(folder.ParentID),
		ResourcePath: folderPath,
		FromPath:     folderPath,
		Metadata: rawJSONMap(map[string]any{
			"recursive": recursive,
		}),
	})
	return &domain.DriveDeleteOperationResult{
		Status:  "deleted",
		Message: "Папка удалена.",
	}, nil
}

func (s *DriveService) UploadFileToFolder(ctx context.Context, userID int64, folderID *uuid.UUID, filename string, size int64, mime string, reader io.Reader) (*domain.DriveFile, error) {
	if strings.TrimSpace(filename) == "" || size < 0 {
		return nil, apperrors.BadRequest("invalid file")
	}

	if folderID != nil {
		if _, err := s.canWorkInFolder(ctx, userID, *folderID, domain.RoleEditor); err != nil {
			return nil, err
		}
	}

	return s.saveDriveFile(ctx, userID, folderID, filename, size, mime, reader, nil, nil, nil)
}

func (s *DriveService) ImportExternalFileToDrive(ctx context.Context, userID int64, req ExternalDriveImportRequest) (*domain.DriveFile, error) {
	payload, err := s.resolveExternalImportPayload(ctx, req)
	if err != nil {
		return nil, err
	}

	filename := strings.TrimSpace(req.AttachmentName)
	if filename == "" {
		attachmentURL := strings.TrimSpace(req.AttachmentURL)
		if parsedURL, parseErr := url.Parse(attachmentURL); parseErr == nil {
			base := filepath.Base(parsedURL.Path)
			if base != "" && base != "." && base != "/" {
				filename = base
			}
		}
	}
	if filename == "" {
		filename = "documentolog-document"
	}

	mimeType := strings.TrimSpace(req.AttachmentMimeType)
	reader := bytes.NewReader(payload)
	if mimeType == "" {
		mimeType, _, err = SniffMimeAndRestoreReader(reader)
		if err != nil {
			return nil, apperrors.InternalWithErr("failed to detect external attachment mime type", err)
		}
		reader = bytes.NewReader(payload)
	}

	sourceCode := optionalNormalizedString(strings.ToUpper(strings.TrimSpace(req.SourceSystemCode)))
	sourceName := optionalNormalizedString(strings.TrimSpace(req.SourceSystemName))
	externalMessageID := optionalNormalizedString(strings.TrimSpace(req.ExternalMessageID))

	return s.saveDriveFile(ctx, userID, req.FolderID, filename, int64(len(payload)), mimeType, reader, sourceCode, sourceName, externalMessageID)
}

func (s *DriveService) resolveExternalImportPayload(ctx context.Context, req ExternalDriveImportRequest) ([]byte, error) {
	if len(req.AttachmentContent) > 0 {
		if int64(len(req.AttachmentContent)) > MaxDocumentSize {
			return nil, apperrors.DriveUploadTooBig(MaxDocumentSize)
		}
		payload := make([]byte, len(req.AttachmentContent))
		copy(payload, req.AttachmentContent)
		return payload, nil
	}

	attachmentURL := strings.TrimSpace(req.AttachmentURL)
	if attachmentURL == "" {
		return nil, apperrors.BadRequest("attachment url or attachment content is required")
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, attachmentURL, nil)
	if err != nil {
		return nil, apperrors.BadRequest("invalid attachment url")
	}

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to download external attachment", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apperrors.BadRequest(fmt.Sprintf("external attachment returned status %d", resp.StatusCode))
	}

	payload, err := io.ReadAll(io.LimitReader(resp.Body, MaxDocumentSize+1))
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to read external attachment", err)
	}
	if int64(len(payload)) > MaxDocumentSize {
		return nil, apperrors.DriveUploadTooBig(MaxDocumentSize)
	}
	return payload, nil
}

func optionalNormalizedString(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	normalized := strings.TrimSpace(value)
	return &normalized
}

// allowIdenticalContent is reserved for explicit Docs creation: blank files and
// template copies may share bytes; ordinary uploads still reject duplicates.
func (s *DriveService) saveDriveFile(ctx context.Context, userID int64, folderID *uuid.UUID, filename string, size int64, mime string, reader io.Reader, sourceSystemCode, sourceSystemName, externalMessageID *string, allowIdenticalContent ...bool) (*domain.DriveFile, error) {
	if strings.TrimSpace(filename) == "" || size < 0 {
		return nil, apperrors.BadRequest("invalid file")
	}

	ext := strings.ToLower(filepath.Ext(filename))
	fileCategory := "file"

	switch {
	case isAudioExt(ext):
		fileCategory = "audio"
	case isVideoExt(ext):
		fileCategory = "video"
	case isImageExt(ext):
		fileCategory = "image"
	case strings.HasPrefix(mime, "audio/"):
		fileCategory = "audio"
	case strings.HasPrefix(mime, "video/"):
		fileCategory = "video"
	case strings.HasPrefix(mime, "image/"):
		fileCategory = "image"
	}

	if err := s.checkDriveUpload(ctx, userID, size, fileCategory); err != nil {
		return nil, err
	}
	hasher := sha256.New()
	teeReader := io.TeeReader(reader, hasher)

	objectName, err := s.fileSvc.UploadFile(ctx, userID, filename, fileCategory, size, teeReader)
	if err != nil {
		return nil, apperrors.ToAppError(err)
	}

	contentHash := hex.EncodeToString(hasher.Sum(nil))
	if duplicate, err := s.repo.FindDuplicateDriveFileInTarget(ctx, userID, folderID, contentHash); err != nil {
		if delErr := s.rollbackDriveUpload(ctx, objectName); delErr != nil {
			s.logger.Warn("failed to rollback uploaded object after duplicate check error", zap.String("object_name", objectName), zap.Error(delErr))
		}
		return nil, apperrors.InternalWithErr("duplicate drive file check failed", err)
	} else if duplicate != nil && !(len(allowIdenticalContent) > 0 && allowIdenticalContent[0]) {
		if delErr := s.rollbackDriveUpload(ctx, objectName); delErr != nil {
			s.logger.Warn("failed to rollback duplicate uploaded object", zap.String("object_name", objectName), zap.Error(delErr))
		}
		return nil, apperrors.Conflict("duplicate file already exists in the target location").
			WithUserMessage("В этой папке уже есть такой же файл. Загрузка отменена.").
			WithDetails(map[string]any{
				"duplicate_file_id":   duplicate.ID.String(),
				"duplicate_file_name": duplicate.RealName,
				"duplicate_folder_id": func() any {
					if duplicate.FolderID == nil {
						return nil
					}
					return duplicate.FolderID.String()
				}(),
			})
	}

	now := time.Now()
	fileID := uuid.New()
	initialOnlyOfficeKey := onlyOfficeDocumentKey(fileID, 1)
	df := &domain.DriveFile{
		ID:                fileID,
		OwnerID:           userID,
		FolderID:          folderID,
		RealName:          filename,
		ObjectName:        objectName,
		ContentHash:       contentHash,
		SourceSystemCode:  sourceSystemCode,
		SourceSystemName:  sourceSystemName,
		ExternalMessageID: externalMessageID,
		Size:              size,
		MimeType:          mime,
		Version:           1,
		Scope:             "drive",
		Key:               &initialOnlyOfficeKey,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	if err := s.repo.CreateFileWithInitialVersion(ctx, df, userID); err != nil {
		if delErr := s.rollbackDriveUpload(ctx, objectName); delErr != nil {
			s.logger.Warn("failed to rollback uploaded object after metadata save error", zap.String("object_name", objectName), zap.Error(delErr))
		}
		s.logger.Error("failed to save file metadata after upload", zap.String("object", objectName))
		return nil, apperrors.InternalWithErr("save file metadata failed", err)
	}
	if resourcePath, err := s.buildFilePath(ctx, folderID, filename); err == nil {
		s.logDriveActivity(ctx, &domain.DriveActivity{
			Action:       domain.DriveActivityActionUploaded,
			ResourceType: domain.ResourceFile,
			ResourceID:   df.ID,
			ResourceName: df.RealName,
			OwnerID:      df.OwnerID,
			ActorID:      userID,
			FolderID:     cloneUUIDPtr(folderID),
			ToFolderID:   cloneUUIDPtr(folderID),
			ResourcePath: resourcePath,
			ToPath:       resourcePath,
			Metadata: rawJSONMap(map[string]any{
				"size":                size,
				"mime_type":           mime,
				"version":             df.Version,
				"source_system_code":  df.SourceSystemCode,
				"source_system_name":  df.SourceSystemName,
				"external_message_id": df.ExternalMessageID,
			}),
		})
	}

	s.recordActivity(userID, domain.ResourceFile, df.ID)

	// Гамификация: загрузка документа / файла в диск
	if s.gamification != nil {
		go func() { _ = s.gamification.TrackActivity(context.Background(), userID, domain.ActionDocumentCreated) }()
	}

	s.fillFileUrl(df)
	s.syncDriveFileIndex(df.ID)
	return df, nil
}

func isVideoExt(ext string) bool {
	switch ext {
	case ".mp4", ".mov", ".avi", ".mkv", ".webm":
		return true
	}
	return false
}

func isImageExt(ext string) bool {
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".svg":
		return true
	}
	return false
}

func isAudioExt(ext string) bool {
	switch ext {
	case ".mp3", ".wav", ".ogg", ".m4a", ".flac":
		return true
	}
	return false
}

func (s *DriveService) GetFileMeta(ctx context.Context, userID int64, fileID uuid.UUID) (*domain.DriveFile, error) {
	f, err := s.canAccessFile(ctx, userID, fileID, domain.RoleViewer)
	if err == nil && f != nil {
		if userID > 0 {
			s.recordActivity(userID, domain.ResourceFile, fileID)
		}
		s.fillFileUrl(f)
	}
	return f, err
}

func (s *DriveService) DownloadFileByID(ctx context.Context, userID int64, fileID uuid.UUID) (io.ReadCloser, string, int64, string, error) {
	meta, err := s.canAccessFile(ctx, userID, fileID, domain.RoleViewer)
	if err != nil {
		return nil, "", 0, "", err
	}

	objectName := meta.ObjectName
	filename := meta.RealName
	mimeType := meta.MimeType

	reader, _, size, err := s.fileSvc.DownloadFile(ctx, objectName)
	if err != nil {
		return nil, "", 0, "", err
	}
	if err := s.auditDriveDownload(ctx, userID, meta, meta.Version, false); err != nil {
		reader.Close()
		return nil, "", 0, "", err
	}
	if userID > 0 {
		s.recordActivity(userID, domain.ResourceFile, fileID)
		if s.gamification != nil && meta.OwnerID != userID {
			go func() { _ = s.gamification.TrackActivity(context.Background(), userID, domain.ActionFileDownloaded) }()
		}
	}

	// Если тип в БД отсутствует или слишком общий (zip/octet-stream),
	// пытаемся определить правильный тип по расширению файла.
	// Это критично для .pptx/.docx/.xlsx, которые могут быть сохранены как application/zip
	if mimeType == "" || mimeType == "application/zip" || mimeType == "application/octet-stream" {
		correctedType := s.fileSvc.GetContentTypeByFilename(filename)
		// Если удалось определить что-то более конкретное, используем это
		if correctedType != "application/octet-stream" {
			mimeType = correctedType
		} else if mimeType == "" {
			mimeType = "application/octet-stream"
		}
	}

	return reader, mimeType, size, filename, nil
}

// GetWOPIFile returns current file metadata after checking the permission encoded
// in the short-lived WOPI access token.
func (s *DriveService) GetWOPIFile(ctx context.Context, claims WOPIAccessClaims, fileID uuid.UUID) (*domain.DriveFile, error) {
	if claims.FileID != fileID.String() {
		return nil, apperrors.Unauthorized("WOPI token file_id mismatch")
	}
	if claims.PublicToken != "" {
		tokenID, err := uuid.Parse(claims.PublicToken)
		if err != nil {
			return nil, apperrors.Unauthorized("invalid WOPI public link")
		}
		item, err := s.GetPublicItemByToken(ctx, tokenID, PublicLinkTarget{Type: domain.ResourceFile, ID: fileID})
		if err != nil {
			return nil, err
		}
		if item.File == nil {
			return nil, apperrors.NotFound("file not found")
		}
		return item.File, nil
	}
	need := domain.RoleViewer
	if claims.CanWrite {
		need = domain.RoleEditor
	}
	return s.canAccessFile(ctx, claims.UserID, fileID, need)
}

// OpenWOPIFile opens the current file stream after rechecking its WOPI session.
func (s *DriveService) OpenWOPIFile(ctx context.Context, claims WOPIAccessClaims, fileID uuid.UUID) (io.ReadCloser, *domain.DriveFile, error) {
	meta, err := s.GetWOPIFile(ctx, claims, fileID)
	if err != nil {
		return nil, nil, err
	}
	reader, _, _, err := s.fileSvc.DownloadFile(ctx, meta.ObjectName)
	if err != nil {
		return nil, nil, err
	}
	return reader, meta, nil
}

// SaveWOPIFile writes a Collabora PutFile payload as a normal Drive revision.
func (s *DriveService) SaveWOPIFile(ctx context.Context, claims WOPIAccessClaims, fileID uuid.UUID, size int64, reader io.Reader) (int, error) {
	if !claims.CanWrite || claims.PublicToken != "" {
		return 0, apperrors.Forbidden("WOPI session is read-only")
	}
	meta, err := s.GetWOPIFile(ctx, claims, fileID)
	if err != nil {
		return 0, err
	}
	return s.UploadFileVersion(ctx, claims.UserID, fileID, meta.RealName, size, reader, meta.Version)
}

func (s *DriveService) UpdateFile(ctx context.Context, userID int64, fileID uuid.UUID, name *string, folderID *uuid.UUID) error {
	file, err := s.canAccessFile(ctx, userID, fileID, domain.RoleEditor)
	if err != nil {
		return err
	}
	originalName := file.RealName
	originalFolderID := cloneUUIDPtr(file.FolderID)
	originalPath, _ := s.buildFilePath(ctx, file.FolderID, file.RealName)
	if folderID != nil {
		if _, err := s.canWorkInFolder(ctx, userID, *folderID, domain.RoleEditor); err != nil {
			return err
		}
	}
	targetFolderID := file.FolderID
	if folderID != nil {
		targetFolderID = folderID
	}
	targetName := file.RealName
	if name != nil {
		targetName = strings.TrimSpace(*name)
		if err := validateDriveName(targetName); err != nil {
			return err
		}
		name = &targetName
	}

	if err := s.repo.UpdateFile(ctx, fileID, name, folderID); err != nil {
		if err.Error() == "file not found" {
			return apperrors.NotFound("file not found")
		}
		return apperrors.Internal("update file failed")
	}
	updatedPath, _ := s.buildFilePath(ctx, targetFolderID, targetName)
	if originalName != targetName {
		s.logDriveActivity(ctx, &domain.DriveActivity{
			Action:       domain.DriveActivityActionRenamed,
			ResourceType: domain.ResourceFile,
			ResourceID:   fileID,
			ResourceName: targetName,
			OwnerID:      file.OwnerID,
			ActorID:      userID,
			FolderID:     cloneUUIDPtr(targetFolderID),
			FromFolderID: cloneUUIDPtr(originalFolderID),
			ToFolderID:   cloneUUIDPtr(targetFolderID),
			ResourcePath: updatedPath,
			FromPath:     originalPath,
			ToPath:       updatedPath,
			Metadata: rawJSONMap(map[string]any{
				"old_name": originalName,
				"new_name": targetName,
			}),
		})
	}
	folderChanged := (originalFolderID == nil) != (targetFolderID == nil)
	if !folderChanged && originalFolderID != nil && targetFolderID != nil {
		folderChanged = *originalFolderID != *targetFolderID
	}
	if folderChanged {
		s.logDriveActivity(ctx, &domain.DriveActivity{
			Action:       domain.DriveActivityActionMoved,
			ResourceType: domain.ResourceFile,
			ResourceID:   fileID,
			ResourceName: targetName,
			OwnerID:      file.OwnerID,
			ActorID:      userID,
			FolderID:     cloneUUIDPtr(targetFolderID),
			FromFolderID: cloneUUIDPtr(originalFolderID),
			ToFolderID:   cloneUUIDPtr(targetFolderID),
			ResourcePath: updatedPath,
			FromPath:     originalPath,
			ToPath:       updatedPath,
		})
	}

	s.recordActivity(userID, domain.ResourceFile, fileID)
	s.syncDriveFileIndex(fileID)
	return nil
}

func (s *DriveService) DeleteFile(ctx context.Context, userID int64, fileID uuid.UUID) (*domain.DriveDeleteOperationResult, error) {
	file, err := s.canAccessFile(ctx, userID, fileID, domain.RoleEditor)
	if err != nil {
		return nil, err
	}
	filePath, _ := s.buildFilePath(ctx, file.FolderID, file.RealName)
	requiresApproval, ownerID, err := s.shouldRequireDeleteApproval(ctx, userID, file.OwnerID, file.FolderID)
	if err != nil {
		return nil, err
	}
	if requiresApproval {
		return s.createOrReuseDeleteRequest(ctx, userID, domain.ResourceFile, file.ID, file.RealName, file.FolderID, ownerID, false)
	}
	if err := s.executeDeleteFile(ctx, fileID); err != nil {
		return nil, err
	}
	s.logDriveActivity(ctx, &domain.DriveActivity{
		Action:       domain.DriveActivityActionDeleted,
		ResourceType: domain.ResourceFile,
		ResourceID:   fileID,
		ResourceName: file.RealName,
		OwnerID:      file.OwnerID,
		ActorID:      userID,
		FolderID:     cloneUUIDPtr(file.FolderID),
		FromFolderID: cloneUUIDPtr(file.FolderID),
		ResourcePath: filePath,
		FromPath:     filePath,
		Metadata: rawJSONMap(map[string]any{
			"version": file.Version,
		}),
	})
	return &domain.DriveDeleteOperationResult{
		Status:  "deleted",
		Message: "Файл удалён.",
	}, nil
}

func (s *DriveService) ApproveDeleteRequest(ctx context.Context, ownerID int64, requestID uuid.UUID) error {
	req, err := s.repo.GetDeleteRequestByID(ctx, requestID)
	if err != nil {
		return apperrors.InternalWithErr("load drive delete request failed", err)
	}
	if req == nil {
		return apperrors.NotFound("delete request not found")
	}
	if req.OwnerID != ownerID {
		return apperrors.Forbidden("only folder owner can approve delete request")
	}
	if req.Status != domain.DriveDeleteRequestPending {
		return apperrors.BadRequest("delete request is already resolved")
	}

	switch req.ResourceType {
	case domain.ResourceFolder:
		folder, err := s.canAccessFolder(ctx, ownerID, req.ResourceID, domain.RoleOwner)
		if err != nil {
			return err
		}
		folderPath, _ := s.buildFolderPathByParent(ctx, folder.ParentID, folder.Name)
		if err := s.executeDeleteFolder(ctx, req.ResourceID, req.Recursive); err != nil {
			return err
		}
		s.logDriveActivity(ctx, &domain.DriveActivity{
			Action:       domain.DriveActivityActionDeleted,
			ResourceType: domain.ResourceFolder,
			ResourceID:   req.ResourceID,
			ResourceName: folder.Name,
			OwnerID:      folder.OwnerID,
			ActorID:      req.RequestedBy,
			FolderID:     cloneUUIDPtr(folder.ParentID),
			FromFolderID: cloneUUIDPtr(folder.ParentID),
			ResourcePath: folderPath,
			FromPath:     folderPath,
			Metadata: rawJSONMap(map[string]any{
				"recursive":   req.Recursive,
				"approved_by": ownerID,
			}),
		})
	case domain.ResourceFile:
		file, err := s.canAccessFile(ctx, ownerID, req.ResourceID, domain.RoleOwner)
		if err != nil {
			return err
		}
		filePath, _ := s.buildFilePath(ctx, file.FolderID, file.RealName)
		if err := s.executeDeleteFile(ctx, req.ResourceID); err != nil {
			return err
		}
		s.logDriveActivity(ctx, &domain.DriveActivity{
			Action:       domain.DriveActivityActionDeleted,
			ResourceType: domain.ResourceFile,
			ResourceID:   req.ResourceID,
			ResourceName: file.RealName,
			OwnerID:      file.OwnerID,
			ActorID:      req.RequestedBy,
			FolderID:     cloneUUIDPtr(file.FolderID),
			FromFolderID: cloneUUIDPtr(file.FolderID),
			ResourcePath: filePath,
			FromPath:     filePath,
			Metadata: rawJSONMap(map[string]any{
				"version":     file.Version,
				"approved_by": ownerID,
			}),
		})
	default:
		return apperrors.BadRequest("invalid delete request resource type")
	}

	now := time.Now()
	if err := s.repo.ResolveDeleteRequest(ctx, req.ID, domain.DriveDeleteRequestExecuted, ownerID, nil, now, &now); err != nil {
		return apperrors.InternalWithErr("resolve drive delete request failed", err)
	}

	resolvedBy := ownerID
	req.Status = domain.DriveDeleteRequestExecuted
	req.ResolvedBy = &resolvedBy
	req.ResolvedAt = &now
	req.ExecutedAt = &now
	s.notifyDriveDeleteRequestResolved(ctx, req, true, "")
	return nil
}

func (s *DriveService) RejectDeleteRequest(ctx context.Context, ownerID int64, requestID uuid.UUID, reason string) error {
	req, err := s.repo.GetDeleteRequestByID(ctx, requestID)
	if err != nil {
		return apperrors.InternalWithErr("load drive delete request failed", err)
	}
	if req == nil {
		return apperrors.NotFound("delete request not found")
	}
	if req.OwnerID != ownerID {
		return apperrors.Forbidden("only folder owner can reject delete request")
	}
	if req.Status != domain.DriveDeleteRequestPending {
		return apperrors.BadRequest("delete request is already resolved")
	}

	now := time.Now()
	trimmedReason := strings.TrimSpace(reason)
	var rejectionReason *string
	if trimmedReason != "" {
		rejectionReason = &trimmedReason
	}

	if err := s.repo.ResolveDeleteRequest(ctx, req.ID, domain.DriveDeleteRequestRejected, ownerID, rejectionReason, now, nil); err != nil {
		return apperrors.InternalWithErr("resolve drive delete request failed", err)
	}

	resolvedBy := ownerID
	req.Status = domain.DriveDeleteRequestRejected
	req.ResolvedBy = &resolvedBy
	req.ResolvedAt = &now
	req.RejectionReason = rejectionReason
	s.notifyDriveDeleteRequestResolved(ctx, req, false, trimmedReason)
	return nil
}

func (s *DriveService) SetFilePublicStatus(ctx context.Context, ownerID int64, fileID uuid.UUID, isPublic bool) error {
	f, err := s.repo.GetFileByID(ctx, fileID)
	if err != nil || f == nil || f.DeletedAt != nil {
		return apperrors.NotFound("file not found")
	}
	if _, err := s.requireDriveManager(ctx, ownerID, domain.ResourceFile, fileID); err != nil {
		return err
	}
	if err := s.repo.SetFilePublicStatus(ctx, fileID, isPublic); err != nil {
		return err
	}
	s.syncDriveFileIndex(fileID)
	return nil
}

func (s *DriveService) SetFolderPublicStatus(ctx context.Context, ownerID int64, folderID uuid.UUID, isPublic bool) error {
	f, err := s.repo.GetFolderByID(ctx, folderID)
	if err != nil || f == nil || f.DeletedAt != nil {
		return apperrors.NotFound("folder not found")
	}
	if _, err := s.requireDriveManager(ctx, ownerID, domain.ResourceFolder, folderID); err != nil {
		return err
	}
	if err := s.repo.SetFolderPublicStatus(ctx, folderID, isPublic); err != nil {
		return err
	}
	s.syncDriveFolderIndex(ctx, folderID)
	return nil
}

func (s *DriveService) GrantPermission(ctx context.Context, ownerID int64, t domain.ResourceType, rid uuid.UUID, targetUserID int64, role domain.PermissionRole) error {
	var roleErr error
	role, roleErr = normalizeDrivePermission(role)
	if roleErr != nil {
		return roleErr
	}
	if err := s.validateDriveRecipients(ctx, []int64{targetUserID}); err != nil {
		return err
	}
	var resourceName string

	switch t {
	case domain.ResourceFolder:
		f, err := s.repo.GetFolderByID(ctx, rid)
		if err != nil || f == nil || f.DeletedAt != nil {
			return apperrors.NotFound("folder not found")
		}
		if _, err := s.requireDriveManager(ctx, ownerID, t, rid); err != nil {
			return err
		}
		if targetUserID == f.OwnerID {
			return apperrors.BadRequest("owner access cannot be changed through ACL")
		}
		resourceName = f.Name
	case domain.ResourceFile:
		f, err := s.repo.GetFileByID(ctx, rid)
		if err != nil || f == nil || f.DeletedAt != nil {
			return apperrors.NotFound("file not found")
		}
		if _, err := s.requireDriveManager(ctx, ownerID, t, rid); err != nil {
			return err
		}
		if targetUserID == f.OwnerID {
			return apperrors.BadRequest("owner access cannot be changed through ACL")
		}
		resourceName = f.RealName
	default:
		return apperrors.BadRequest("invalid resource type")
	}

	p := &domain.Permission{
		ResourceType: t,
		ResourceID:   rid,
		UserID:       targetUserID,
		Role:         role,
		CreatedAt:    time.Now(),
	}
	if err := s.repo.UpsertPermission(ctx, p); err != nil {
		return apperrors.Internal("grant permission failed")
	}
	if t == domain.ResourceFile {
		s.syncDriveFileIndex(rid)
	} else {
		s.queueDriveFolderIndexSync(rid)
	}

	// Гамификация: поделился файлом / папкой
	if s.gamification != nil {
		go func(oID int64) { _ = s.gamification.TrackActivity(context.Background(), oID, domain.ActionFileShared) }(ownerID)
	}

	if s.notifService == nil {
		return nil
	}
	go func() {
		asyncCtx := context.Background()

		resTypeStr := "файл"
		if t == domain.ResourceFolder {
			resTypeStr = "папку"
		}

		roleStr := "просмотр"
		if role == domain.RoleEditor {
			roleStr = "редактирование"
		} else if role == domain.RoleOwner {
			roleStr = "управление доступом"
		}

		notifReq := domain.CreateNotificationRequest{
			UserID:          targetUserID,
			Type:            "drive_access_granted",
			TitleTemplate:   &domain.NotificationTemplate{Source: "Вам предоставлен доступ", Args: []any{}},
			Title:           "Вам предоставлен доступ",
			ContentTemplate: &domain.NotificationTemplate{Source: "Вам открыли доступ (%s) на %s: %s", Args: []any{domain.NotificationLabel(roleStr), domain.NotificationLabel(resTypeStr), resourceName}},
			Content:         fmt.Sprintf("Вам открыли доступ (%s) на %s: %s", roleStr, resTypeStr, resourceName),
			Metadata: map[string]string{
				"resource_id":   rid.String(),
				"resource_type": string(t),
				"role":          string(role),
			},
		}

		if err := s.notifService.CreateAndSendNotification(asyncCtx, notifReq); err != nil {
			s.logger.Error("failed to send drive access notification",
				zap.Int64("target_user_id", targetUserID),
				zap.String("resource_id", rid.String()),
				zap.Error(err),
			)
		}
	}()

	return nil
}

func (s *DriveService) RevokePermission(ctx context.Context, ownerID int64, t domain.ResourceType, rid uuid.UUID, targetUserID int64) error {
	switch t {
	case domain.ResourceFolder:
		f, err := s.repo.GetFolderByID(ctx, rid)
		if err != nil || f == nil || f.DeletedAt != nil {
			return apperrors.NotFound("folder not found")
		}
		if _, err := s.requireDriveManager(ctx, ownerID, t, rid); err != nil {
			return err
		}
		if targetUserID == f.OwnerID {
			return apperrors.BadRequest("owner access cannot be changed through ACL")
		}
	case domain.ResourceFile:
		f, err := s.repo.GetFileByID(ctx, rid)
		if err != nil || f == nil || f.DeletedAt != nil {
			return apperrors.NotFound("file not found")
		}
		if _, err := s.requireDriveManager(ctx, ownerID, t, rid); err != nil {
			return err
		}
		if targetUserID == f.OwnerID {
			return apperrors.BadRequest("owner access cannot be changed through ACL")
		}
	default:
		return apperrors.BadRequest("invalid resource_type")
	}

	if err := s.repo.DeletePermission(ctx, t, rid, targetUserID); err != nil {
		return apperrors.Internal("revoke permission failed")
	}
	if t == domain.ResourceFile {
		s.syncDriveFileIndex(rid)
	} else {
		s.queueDriveFolderIndexSync(rid)
	}
	return nil
}

func (s *DriveService) RevokePermissionsBulk(ctx context.Context, ownerID int64, t domain.ResourceType, rid uuid.UUID, targetUserIDs []int64) error {
	targetUserIDs = uniquePositiveUserIDs(targetUserIDs)
	if len(targetUserIDs) == 0 {
		return apperrors.BadRequest("user_id is required")
	}

	switch t {
	case domain.ResourceFolder:
		f, err := s.repo.GetFolderByID(ctx, rid)
		if err != nil || f == nil || f.DeletedAt != nil {
			return apperrors.NotFound("folder not found")
		}
		if _, err := s.requireDriveManager(ctx, ownerID, t, rid); err != nil {
			return err
		}
		for _, id := range targetUserIDs {
			if id == f.OwnerID {
				return apperrors.BadRequest("owner access cannot be changed through ACL")
			}
		}
	case domain.ResourceFile:
		f, err := s.repo.GetFileByID(ctx, rid)
		if err != nil || f == nil || f.DeletedAt != nil {
			return apperrors.NotFound("file not found")
		}
		if _, err := s.requireDriveManager(ctx, ownerID, t, rid); err != nil {
			return err
		}
		for _, id := range targetUserIDs {
			if id == f.OwnerID {
				return apperrors.BadRequest("owner access cannot be changed through ACL")
			}
		}
	default:
		return apperrors.BadRequest("invalid resource_type")
	}

	if err := s.repo.DeletePermissionsBulk(ctx, t, rid, targetUserIDs); err != nil {
		return apperrors.Internal("revoke permission failed")
	}
	if t == domain.ResourceFile {
		s.syncDriveFileIndex(rid)
	} else {
		s.queueDriveFolderIndexSync(rid)
	}
	return nil
}

func uniquePositiveUserIDs(userIDs []int64) []int64 {
	if len(userIDs) == 0 {
		return nil
	}

	out := make([]int64, 0, len(userIDs))
	seen := make(map[int64]struct{}, len(userIDs))
	for _, userID := range userIDs {
		if userID <= 0 {
			continue
		}
		if _, exists := seen[userID]; exists {
			continue
		}
		seen[userID] = struct{}{}
		out = append(out, userID)
	}
	return out
}

func (s *DriveService) sendDriveAccessNotifications(t domain.ResourceType, rid uuid.UUID, resourceName string, role domain.PermissionRole, targetUserIDs []int64) {
	if s.notifService == nil || len(targetUserIDs) == 0 {
		return
	}

	go func() {
		asyncCtx := context.Background()

		resTypeStr := "файл"
		if t == domain.ResourceFolder {
			resTypeStr = "папку"
		}

		roleStr := "просмотр"
		if role == domain.RoleEditor {
			roleStr = "редактирование"
		} else if role == domain.RoleOwner {
			roleStr = "управление доступом"
		}

		for _, targetUserID := range targetUserIDs {
			notifReq := domain.CreateNotificationRequest{
				UserID:          targetUserID,
				Type:            "drive_access_granted",
				TitleTemplate:   &domain.NotificationTemplate{Source: "Вам предоставлен доступ", Args: []any{}},
				Title:           "Вам предоставлен доступ",
				ContentTemplate: &domain.NotificationTemplate{Source: "Вам открыли доступ (%s) на %s: %s", Args: []any{domain.NotificationLabel(roleStr), domain.NotificationLabel(resTypeStr), resourceName}},
				Content:         fmt.Sprintf("Вам открыли доступ (%s) на %s: %s", roleStr, resTypeStr, resourceName),
				Metadata: map[string]string{
					"resource_id":   rid.String(),
					"resource_type": string(t),
					"role":          string(role),
				},
			}

			if err := s.notifService.CreateAndSendNotification(asyncCtx, notifReq); err != nil {
				s.logger.Error("failed to send drive access notification",
					zap.Int64("target_user_id", targetUserID),
					zap.String("resource_id", rid.String()),
					zap.Error(err),
				)
			}
		}
	}()
}

func (s *DriveService) TransferOwnership(ctx context.Context, ownerID int64, t domain.ResourceType, rid uuid.UUID, newOwnerID int64) error {
	if ownerID == 0 {
		return apperrors.Unauthorized("unauthorized")
	}
	if newOwnerID == 0 {
		return apperrors.BadRequest("new_owner_id is required")
	}
	if ownerID == newOwnerID {
		return apperrors.BadRequest("new owner must be different from current owner")
	}

	exists, err := s.repo.UserExists(ctx, newOwnerID)
	if err != nil {
		return apperrors.Internal("failed to validate new owner")
	}
	if !exists {
		return apperrors.NotFound("new owner not found")
	}

	switch t {
	case domain.ResourceFolder:
		f, err := s.repo.GetFolderByID(ctx, rid)
		if err != nil || f == nil || f.DeletedAt != nil {
			return apperrors.NotFound("folder not found")
		}
		if f.OwnerID != ownerID {
			return apperrors.Forbidden("only owner can transfer ownership")
		}
		if err := s.repo.TransferFolderOwnership(ctx, rid, ownerID, newOwnerID, domain.RoleEditor); err != nil {
			if err.Error() == "folder not found" {
				return apperrors.NotFound("folder not found")
			}
			return apperrors.InternalWithErr("transfer folder ownership failed", err)
		}
		s.syncDriveFolderIndex(ctx, rid)
		s.sendOwnershipTransferNotifications(rid, t, f.Name, ownerID, newOwnerID)
		return nil
	case domain.ResourceFile:
		f, err := s.repo.GetFileByID(ctx, rid)
		if err != nil || f == nil || f.DeletedAt != nil {
			return apperrors.NotFound("file not found")
		}
		if f.OwnerID != ownerID {
			return apperrors.Forbidden("only owner can transfer ownership")
		}
		if err := s.repo.TransferFileOwnership(ctx, rid, ownerID, newOwnerID, domain.RoleEditor); err != nil {
			if err.Error() == "file not found" {
				return apperrors.NotFound("file not found")
			}
			return apperrors.InternalWithErr("transfer file ownership failed", err)
		}
		s.syncDriveFileIndex(rid)
		s.sendOwnershipTransferNotifications(rid, t, f.RealName, ownerID, newOwnerID)
		return nil
	default:
		return apperrors.BadRequest("invalid resource_type")
	}
}

func (s *DriveService) sendOwnershipTransferNotifications(resourceID uuid.UUID, resourceType domain.ResourceType, resourceName string, previousOwnerID, newOwnerID int64) {
	if s.notifService == nil {
		return
	}

	go func() {
		asyncCtx := context.Background()
		resourceLabel := "файл"
		if resourceType == domain.ResourceFolder {
			resourceLabel = "папка"
		}

		if err := s.notifService.CreateAndSendNotification(asyncCtx, domain.CreateNotificationRequest{
			UserID:          newOwnerID,
			Type:            "drive_ownership_transferred",
			TitleTemplate:   &domain.NotificationTemplate{Source: "Вам передали владение", Args: []any{}},
			Title:           "Вам передали владение",
			ContentTemplate: &domain.NotificationTemplate{Source: "Вам передали владение на %s: %s", Args: []any{domain.NotificationLabel(resourceLabel), resourceName}},
			Content:         fmt.Sprintf("Вам передали владение на %s: %s", resourceLabel, resourceName),
			Metadata: map[string]string{
				"resource_id":   resourceID.String(),
				"resource_type": string(resourceType),
				"role":          string(domain.RoleOwner),
			},
		}); err != nil {
			s.logger.Error("failed to notify new drive owner",
				zap.Int64("new_owner_id", newOwnerID),
				zap.String("resource_id", resourceID.String()),
				zap.Error(err),
			)
		}

		if err := s.notifService.CreateAndSendNotification(asyncCtx, domain.CreateNotificationRequest{
			UserID:          previousOwnerID,
			Type:            "drive_ownership_transferred",
			TitleTemplate:   &domain.NotificationTemplate{Source: "Владение передано", Args: []any{}},
			Title:           "Владение передано",
			ContentTemplate: &domain.NotificationTemplate{Source: "Вы передали владение на %s: %s. Ваш доступ сохранён как editor.", Args: []any{domain.NotificationLabel(resourceLabel), resourceName}},
			Content:         fmt.Sprintf("Вы передали владение на %s: %s. Ваш доступ сохранён как editor.", resourceLabel, resourceName),
			Metadata: map[string]string{
				"resource_id":   resourceID.String(),
				"resource_type": string(resourceType),
				"role":          string(domain.RoleEditor),
			},
		}); err != nil {
			s.logger.Error("failed to notify previous drive owner",
				zap.Int64("previous_owner_id", previousOwnerID),
				zap.String("resource_id", resourceID.String()),
				zap.Error(err),
			)
		}
	}()
}

type OnlyOfficeCallback struct {
	FileType string `json:"filetype,omitempty"`
	Status   int    `json:"status"`
	URL      string `json:"url"`
	Key      string `json:"key"`
	Token    string `json:"token,omitempty"`
}

func (s *DriveService) SaveOnlyOfficeCallback(ctx context.Context, editorID int64, fileID uuid.UUID, cb OnlyOfficeCallback) (saveErr error) {
	if cb.Status != 2 && cb.Status != 6 {
		s.logger.Info("ignoring onlyoffice callback with unsupported status",
			zap.String("file_id", fileID.String()),
			zap.Int64("editor_id", editorID),
			zap.Int("status", cb.Status),
			zap.String("key", cb.Key),
			zap.String("url", cb.URL),
		)
		return nil
	}
	if strings.TrimSpace(cb.URL) == "" {
		return apperrors.BadRequest("callback url is empty")
	}

	s.logger.Info("processing onlyoffice save callback",
		zap.String("file_id", fileID.String()),
		zap.Int64("editor_id", editorID),
		zap.Int("status", cb.Status),
		zap.String("key", cb.Key),
		zap.String("url", cb.URL),
		zap.Bool("has_token", strings.TrimSpace(cb.Token) != ""),
	)

	if editorID <= 0 {
		return apperrors.Unauthorized("authenticated editor required")
	}
	meta, err := s.canAccessFile(ctx, editorID, fileID, domain.RoleEditor)
	if err != nil {
		return err
	}

	if cb.FileType != "" && !strings.EqualFold(cb.FileType, strings.TrimPrefix(filepath.Ext(meta.RealName), ".")) {
		return apperrors.Conflict("saved format differs from Drive file extension")
	}
	if strings.TrimSpace(cb.Key) == "" {
		return apperrors.BadRequest("document key required")
	}
	eventHash := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s", cb.Status, cb.URL)))
	event := hex.EncodeToString(eventHash[:])
	applied, err := s.repo.OfficeSaveApplied(ctx, fileID, cb.Key, event)
	if err != nil {
		return apperrors.InternalWithErr("read office save receipt", err)
	}
	if applied {
		return nil
	}
	if cb.Key != activeOnlyOfficeKey(meta) {
		return apperrors.Conflict("document changed outside this editor session; reopen it")
	}

	// Persist a bounded, non-sensitive failure marker even if the request was cancelled.
	// Version/key matching prevents a late failure overwriting a newer checkpoint.
	defer func() {
		if saveErr == nil {
			return
		}
		failureCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		if err := s.repo.RecordOfficeSaveFailure(failureCtx, fileID, cb.Key, meta.Version, "save_failed"); err != nil {
			s.logger.Error("record office save failure", zap.Error(err))
		}
	}()

	effectiveEditorID := editorID

	client := &http.Client{Timeout: 60 * time.Second}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cb.URL, nil)
	if err != nil {
		return apperrors.Internal("failed to create download request")
	}

	if strings.TrimSpace(cb.Token) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(cb.Token))
	}

	resp, err := client.Do(req)
	if err != nil {
		s.logger.Error("OnlyOffice download request failed",
			zap.Error(err),
			zap.String("url", cb.URL),
			zap.String("key", cb.Key),
		)
		return apperrors.Internal("failed to download edited file").WithDetails(map[string]any{
			"url":  cb.URL,
			"key":  cb.Key,
			"err":  err.Error(),
			"hint": "network/timeout/DNS or DocumentServer not reachable from backend",
		})
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
		bodyStr := strings.TrimSpace(string(bodyBytes))
		hint := hintByStatus(resp.StatusCode, bodyStr)

		s.logger.Error("OnlyOffice download failed",
			zap.Int("status", resp.StatusCode),
			zap.String("body", bodyStr),
			zap.String("url", cb.URL),
			zap.String("key", cb.Key),
		)

		return apperrors.Internal("onlyoffice download failed").WithDetails(map[string]any{
			"http_status": resp.StatusCode,
			"url":         cb.URL,
			"key":         cb.Key,
			"body":        shorten(bodyStr, 1500),
			"hint":        hint,
		})
	}

	size := resp.ContentLength
	var reader io.Reader = resp.Body
	if size < 0 {
		storage, err := s.GetDriveStorage(ctx, meta.OwnerID)
		if err != nil {
			return err
		}
		limit := driveMinBytes(storage.FreeBytes, storage.UploadLimits["file"])
		temporary, err := os.CreateTemp("", "drive-office-*")
		if err != nil {
			return apperrors.InternalWithErr("create edited file buffer failed", err)
		}
		defer os.Remove(temporary.Name())
		defer temporary.Close()
		size, err = io.Copy(temporary, io.LimitReader(resp.Body, limit+1))
		if err != nil {
			return apperrors.InternalWithErr("read edited file failed", err)
		}
		// Reading limit+1 signals a truncated body. Never accept it even if
		// another request frees space (or an operator raises the quota) now.
		if size > limit {
			if limit == storage.UploadLimits["file"] {
				return apperrors.DriveUploadTooBig(limit)
			}
			return apperrors.DriveQuotaExceeded(map[string]any{"quota_bytes": storage.QuotaBytes, "used_bytes": storage.UsedBytes, "free_bytes": storage.FreeBytes, "requested_bytes": size})
		}
		if err = s.checkDriveUpload(ctx, meta.OwnerID, size, "file"); err != nil {
			return err
		}
		if _, err = temporary.Seek(0, io.SeekStart); err != nil {
			return apperrors.InternalWithErr("read edited file buffer failed", err)
		}
		reader = temporary
	}
	if err := s.checkDriveUpload(ctx, meta.OwnerID, size, "file"); err != nil {
		return err
	}

	newObject, err := s.fileSvc.UploadFile(ctx, effectiveEditorID, meta.RealName, "file", size, reader)
	if err != nil {
		s.logger.Error("failed to upload onlyoffice-edited file to storage",
			zap.String("file_id", fileID.String()),
			zap.String("key", cb.Key),
			zap.String("url", cb.URL),
			zap.Int64("editor_id", effectiveEditorID),
			zap.Int64("bytes", size),
			zap.Error(err),
		)
		return apperrors.ToAppError(err)
	}

	// Recheck access after network IO, including deletion or moved-folder ACLs.
	current, err := s.canAccessFile(ctx, editorID, fileID, domain.RoleEditor)
	if err != nil {
		_ = s.rollbackDriveUpload(ctx, newObject)
		return err
	}
	newVersion, applied, err := s.repo.AppendOfficeVersion(ctx, fileID, newObject, size, effectiveEditorID, meta.Version, cb.Key, event, cb.Status == 2)
	if err != nil {
		s.logger.Error("failed to persist onlyoffice-edited file version",
			zap.String("file_id", fileID.String()),
			zap.String("previous_object", meta.ObjectName),
			zap.String("new_object", newObject),
			zap.Int64("editor_id", effectiveEditorID),
			zap.Int64("bytes", size),
			zap.Error(err),
		)
		if errors.Is(err, repository.ErrDriveVersionConflict) {
			_ = s.rollbackDriveUpload(ctx, newObject)
			return apperrors.Conflict("file changed while saving; retry or reopen the document")
		}
		appErr := apperrors.InternalWithErr("update file version failed", err)
		if appErr.StatusCode == http.StatusInsufficientStorage || appErr.StatusCode == http.StatusRequestEntityTooLarge || appErr.StatusCode == http.StatusConflict {
			_ = s.rollbackDriveUpload(ctx, newObject)
		}
		return appErr
	}
	if !applied {
		_ = s.rollbackDriveUpload(ctx, newObject)
		return nil
	}
	meta.FolderID = current.FolderID
	meta.RealName = current.RealName
	meta.OwnerID = current.OwnerID
	filePath, _ := s.buildFilePath(ctx, meta.FolderID, meta.RealName)
	s.logDriveActivity(ctx, &domain.DriveActivity{
		Action:       domain.DriveActivityActionEdited,
		ResourceType: domain.ResourceFile,
		ResourceID:   fileID,
		ResourceName: meta.RealName,
		OwnerID:      meta.OwnerID,
		ActorID:      effectiveEditorID,
		FolderID:     cloneUUIDPtr(meta.FolderID),
		ResourcePath: filePath,
		ToPath:       filePath,
		Metadata: rawJSONMap(map[string]any{
			"previous_version": meta.Version,
			"new_version":      newVersion,
			"size":             size,
		}),
	})

	s.recordActivity(effectiveEditorID, domain.ResourceFile, fileID)
	s.syncDriveFileIndex(fileID)

	s.logger.Info("onlyoffice callback saved new drive file version",
		zap.String("file_id", fileID.String()),
		zap.String("previous_object", meta.ObjectName),
		zap.String("new_object", newObject),
		zap.Int64("editor_id", effectiveEditorID),
		zap.Int64("bytes", size),
	)

	return nil
}

func (s *DriveService) GrantPermissionsBulk(ctx context.Context, ownerID int64, t domain.ResourceType, rid uuid.UUID, targetUserIDs []int64, role domain.PermissionRole) error {
	var roleErr error
	role, roleErr = normalizeDrivePermission(role)
	if roleErr != nil {
		return roleErr
	}
	targetUserIDs = uniquePositiveUserIDs(targetUserIDs)
	if len(targetUserIDs) == 0 {
		return apperrors.BadRequest("user_id is required")
	}

	if err := s.validateDriveRecipients(ctx, targetUserIDs); err != nil {
		return err
	}
	var resourceName string

	switch t {
	case domain.ResourceFolder:
		f, err := s.repo.GetFolderByID(ctx, rid)
		if err != nil || f == nil || f.DeletedAt != nil {
			return apperrors.NotFound("folder not found")
		}
		if _, err := s.requireDriveManager(ctx, ownerID, t, rid); err != nil {
			return err
		}
		for _, id := range targetUserIDs {
			if id == f.OwnerID {
				return apperrors.BadRequest("owner access cannot be changed through ACL")
			}
		}
		resourceName = f.Name
	case domain.ResourceFile:
		f, err := s.repo.GetFileByID(ctx, rid)
		if err != nil || f == nil || f.DeletedAt != nil {
			return apperrors.NotFound("file not found")
		}
		if _, err := s.requireDriveManager(ctx, ownerID, t, rid); err != nil {
			return err
		}
		for _, id := range targetUserIDs {
			if id == f.OwnerID {
				return apperrors.BadRequest("owner access cannot be changed through ACL")
			}
		}
		resourceName = f.RealName
	default:
		return apperrors.BadRequest("invalid resource type")
	}

	if err := s.repo.UpsertPermissionsBulk(ctx, t, rid, targetUserIDs, role, time.Now()); err != nil {
		return apperrors.Internal("grant permission failed")
	}
	if t == domain.ResourceFile {
		s.syncDriveFileIndex(rid)
	} else {
		s.queueDriveFolderIndexSync(rid)
	}

	if s.gamification != nil {
		go func(oID int64) { _ = s.gamification.TrackActivity(context.Background(), oID, domain.ActionFileShared) }(ownerID)
	}

	s.sendDriveAccessNotifications(t, rid, resourceName, role, targetUserIDs)
	return nil
}

func shorten(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}

func hintByStatus(code int, body string) string {
	switch code {
	case 401:
		return "401 Unauthorized: DocumentServer требует авторизацию/JWT. Проверь OnlyOffice JWT настройки и что ты передаешь token (если нужен)."
	case 403:
		return "403 Forbidden: Nginx/OnlyOffice блокирует доступ к URL (IP allowlist/deny, request filtering, либо доступ к /cache запрещен извне). Проверь nginx конфиг DocumentServer и allowlist."
	case 404:
		return "404 Not Found: URL устарел или файл уже очищен из cache. Проверь как долго хранится cache и не меняется ли ссылка."
	default:
		if code >= 500 {
			return "5xx от DocumentServer: внутренняя ошибка OnlyOffice/nginx. Смотри логи onlyoffice/documentserver."
		}
	}
	if strings.Contains(strings.ToLower(body), "nginx") && code == 403 {
		return "Похоже на nginx 403: скорее всего deny/allowlist на стороне reverse-proxy."
	}
	return "Unexpected non-2xx from DocumentServer. Check response body + DocumentServer logs."
}

func SniffMimeAndRestoreReader(r io.Reader) (string, io.Reader, error) {
	buf := make([]byte, 512)
	n, err := io.ReadFull(r, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return "", nil, err
	}
	buf = buf[:n]
	mime := http.DetectContentType(buf)
	return mime, io.MultiReader(bytes.NewReader(buf), r), nil
}

func (s *DriveService) fillFileUrl(f *domain.DriveFile) {
	if f == nil {
		return
	}
	if f.ObjectName != "" {
		f.DownloadURL = fmt.Sprintf("%s/api/v1/files/download?object_name=%s", s.publicURL, f.ObjectName)
	}
	if f.ID != uuid.Nil && f.Version > 0 {
		key := onlyOfficeDocumentKey(f.ID, f.Version)
		f.Key = &key
	}
}

func onlyOfficeDocumentKey(fileID uuid.UUID, version int) string {
	if version <= 0 {
		version = 1
	}
	return fmt.Sprintf("%s-v%d", fileID.String(), version)
}

func (s *DriveService) ListOwnItems(ctx context.Context, userID int64, limit, offset int) ([]domain.DriveItem, error) {
	folders, err := s.repo.ListRootFoldersByOwnerPaginated(ctx, userID, limit, offset)
	if err != nil {
		return nil, apperrors.Internal("list own root folders failed")
	}
	files, err := s.repo.ListRootFilesByOwnerPaginated(ctx, userID, limit, offset)
	if err != nil {
		return nil, apperrors.Internal("list own root files failed")
	}

	own := make([]domain.DriveItem, 0, len(folders)+len(files))

	for _, f := range folders {
		own = append(own, domain.DriveItem{Type: domain.ResourceFolder, Folder: f})
	}

	for _, f := range files {
		s.fillFileUrl(f)

		own = append(own, domain.DriveItem{Type: domain.ResourceFile, File: f})
	}

	sort.Slice(own, func(i, j int) bool {
		var t1, t2 time.Time
		if own[i].Type == domain.ResourceFolder {
			t1 = own[i].Folder.UpdatedAt
		} else {
			t1 = own[i].File.UpdatedAt
		}
		if own[j].Type == domain.ResourceFolder {
			t2 = own[j].Folder.UpdatedAt
		} else {
			t2 = own[j].File.UpdatedAt
		}
		return t1.After(t2)
	})

	return own, nil
}

func (s *DriveService) ListSharedItems(ctx context.Context, userID int64, limit, offset int) ([]domain.DriveItem, error) {
	folders, err := s.repo.ListSharedFoldersPaginated(ctx, userID, limit, offset)
	if err != nil {
		return nil, apperrors.Internal("list shared folders failed")
	}
	files, err := s.repo.ListSharedFilesPaginated(ctx, userID, limit, offset)
	if err != nil {
		return nil, apperrors.Internal("list shared files failed")
	}

	shared := make([]domain.DriveItem, 0, len(folders)+len(files))
	for _, f := range folders {
		shared = append(shared, domain.DriveItem{Type: domain.ResourceFolder, Folder: f})
	}
	for _, f := range files {
		shared = append(shared, domain.DriveItem{Type: domain.ResourceFile, File: f})
	}
	return shared, nil
}

func (s *DriveService) ListAllDriveItems(ctx context.Context, userID int64, limit, offset int) ([]domain.DriveItem, []domain.DriveItem, error) {
	ownFolders, err := s.repo.ListRootFoldersByOwnerPaginated(ctx, userID, limit, offset)
	if err != nil {
		return nil, nil, apperrors.Internal("list own root folders failed")
	}
	ownFiles, err := s.repo.ListRootFilesByOwnerPaginated(ctx, userID, limit, offset)
	if err != nil {
		return nil, nil, apperrors.Internal("list own root files failed")
	}

	own := make([]domain.DriveItem, 0, len(ownFolders)+len(ownFiles))
	for _, f := range ownFolders {
		own = append(own, domain.DriveItem{Type: domain.ResourceFolder, Folder: f})
	}
	for _, f := range ownFiles {
		own = append(own, domain.DriveItem{Type: domain.ResourceFile, File: f})
	}

	sharedFolders, err := s.repo.ListSharedFoldersPaginated(ctx, userID, limit, offset)
	if err != nil {
		return nil, nil, apperrors.Internal("list shared folders failed")
	}
	sharedFiles, err := s.repo.ListSharedFilesPaginated(ctx, userID, limit, offset)
	if err != nil {
		return nil, nil, apperrors.Internal("list shared files failed")
	}

	shared := make([]domain.DriveItem, 0, len(sharedFolders)+len(sharedFiles))
	for _, f := range sharedFolders {
		shared = append(shared, domain.DriveItem{Type: domain.ResourceFolder, Folder: f})
	}
	for _, f := range sharedFiles {
		shared = append(shared, domain.DriveItem{Type: domain.ResourceFile, File: f})
	}

	return own, shared, nil
}

func (s *DriveService) GetUsersWithAccess(ctx context.Context, userID int64, resourceType domain.ResourceType, resourceID uuid.UUID) ([]domain.UserPermission, error) {
	users, _, err := s.GetUsersWithAccessPaginated(ctx, userID, resourceType, resourceID, 0, 0, "")
	return users, err
}

func (s *DriveService) GetUsersWithAccessPaginated(ctx context.Context, userID int64, resourceType domain.ResourceType, resourceID uuid.UUID, limit, offset int, searchQuery string) ([]domain.UserPermission, int, error) {
	switch resourceType {
	case domain.ResourceFile:
		_, err := s.canAccessFile(ctx, userID, resourceID, domain.RoleViewer)
		if err != nil {
			return nil, 0, err
		}
	case domain.ResourceFolder:
		_, err := s.canAccessFolder(ctx, userID, resourceID, domain.RoleViewer)
		if err != nil {
			return nil, 0, err
		}
	default:
		return nil, 0, apperrors.BadRequest("invalid resource type")
	}

	users, total, err := s.repo.ListUsersWithAccessPaginated(ctx, resourceType, resourceID, limit, offset, searchQuery)
	if err != nil {
		return nil, 0, apperrors.Internal("failed to list users with access")
	}

	return users, total, nil
}

func (s *DriveService) BuildFolderAccessReportCSV(ctx context.Context, userID int64, folderID uuid.UUID) ([]byte, string, error) {
	folder, err := s.canAccessFolder(ctx, userID, folderID, domain.RoleOwner)
	if err != nil {
		return nil, "", err
	}

	entries, err := s.repo.ListFolderAccessReportEntries(ctx, folderID)
	if err != nil {
		return nil, "", apperrors.InternalWithErr("failed to build folder access report", err)
	}

	file := excelize.NewFile()
	const sheetName = "Access Report"
	defaultSheet := file.GetSheetName(file.GetActiveSheetIndex())
	if defaultSheet != sheetName {
		if err := file.SetSheetName(defaultSheet, sheetName); err != nil {
			return nil, "", apperrors.InternalWithErr("failed to set folder access report sheet name", err)
		}
	}

	headers := []string{"Папка", "Полный путь папки", "ФИО", "Email", "Логин", "Роль"}
	for i, header := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		if err := file.SetCellValue(sheetName, cell, header); err != nil {
			return nil, "", apperrors.InternalWithErr("failed to write folder access report header", err)
		}
	}

	headerStyle, err := file.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true},
		Alignment: &excelize.Alignment{Horizontal: "center"},
	})
	if err == nil {
		_ = file.SetCellStyle(sheetName, "A1", "F1", headerStyle)
	}

	seenRows := make(map[string]struct{}, len(entries))
	rowIndex := 2
	for _, entry := range entries {
		fullName := strings.TrimSpace(entry.FirstName + " " + entry.LastName)
		if fullName == "" {
			fullName = entry.Username
		}
		row := []interface{}{
			entry.FolderName,
			entry.FolderPath,
			fullName,
			entry.Email,
			entry.Username,
			string(entry.Role),
		}
		rowKey := strings.Join([]string{
			entry.FolderName,
			entry.FolderPath,
			fullName,
			entry.Email,
			entry.Username,
			string(entry.Role),
		}, "\x1f")
		if _, exists := seenRows[rowKey]; exists {
			continue
		}
		seenRows[rowKey] = struct{}{}

		cell, _ := excelize.CoordinatesToCellName(1, rowIndex)
		if err := file.SetSheetRow(sheetName, cell, &row); err != nil {
			return nil, "", apperrors.InternalWithErr("failed to write folder access report row", err)
		}
		rowIndex++
	}

	_ = file.SetColWidth(sheetName, "A", "B", 28)
	_ = file.SetColWidth(sheetName, "C", "C", 32)
	_ = file.SetColWidth(sheetName, "D", "E", 30)
	_ = file.SetColWidth(sheetName, "F", "F", 14)

	buf, err := file.WriteToBuffer()
	if err != nil {
		return nil, "", apperrors.InternalWithErr("failed to finalize folder access report", err)
	}

	return buf.Bytes(), fmt.Sprintf("%s-access-report.xlsx", sanitizeDriveReportFileName(folder.Name)), nil
}

func sanitizeDriveReportFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "folder"
	}
	replacer := strings.NewReplacer(
		"/", "-",
		"\\", "-",
		":", "-",
		"*", "",
		"?", "",
		"\"", "",
		"<", "",
		">", "",
		"|", "",
	)
	name = replacer.Replace(name)
	name = strings.TrimSpace(name)
	if name == "" {
		return "folder"
	}
	return name
}

func (s *DriveService) GetUsersWithoutAccess(
	ctx context.Context,
	userID int64,
	resourceType domain.ResourceType,
	resourceID uuid.UUID,
	query string,
) ([]domain.UserBasic, error) {
	users, _, err := s.GetUsersWithoutAccessPaginated(ctx, userID, resourceType, resourceID, query, 0, 0)
	return users, err
}

func (s *DriveService) GetUsersWithoutAccessPaginated(
	ctx context.Context,
	userID int64,
	resourceType domain.ResourceType,
	resourceID uuid.UUID,
	query string,
	limit, offset int,
) ([]domain.UserBasic, int, error) {
	switch resourceType {
	case domain.ResourceFile:
		_, err := s.canAccessFile(ctx, userID, resourceID, domain.RoleViewer)
		if err != nil {
			return nil, 0, err
		}
	case domain.ResourceFolder:
		_, err := s.canAccessFolder(ctx, userID, resourceID, domain.RoleViewer)
		if err != nil {
			return nil, 0, err
		}
	default:
		return nil, 0, apperrors.BadRequest("invalid resource type")
	}

	users, total, err := s.repo.ListUsersWithoutAccessPaginated(ctx, userID, resourceType, resourceID, query, limit, offset)
	if err != nil {
		return nil, 0, apperrors.Internal("failed to list available users")
	}

	return users, total, nil
}

func (s *DriveService) ListRecent(ctx context.Context, userID int64, limit, offset int) ([]domain.RecentItem, error) {
	items, err := s.recentRepo.ListRecent(ctx, userID, limit, offset)
	if err != nil {
		return nil, err
	}

	filtered := make([]domain.RecentItem, 0, len(items))
	for _, item := range items {
		switch item.Type {
		case domain.ResourceFolder:
			if item.Folder == nil {
				continue
			}
			if _, err := s.canWorkInFolder(ctx, userID, item.Folder.ID, domain.RoleViewer); err == nil {
				filtered = append(filtered, item)
			}
		case domain.ResourceFile:
			if item.File == nil {
				continue
			}
			if _, err := s.canAccessFile(ctx, userID, item.File.ID, domain.RoleViewer); err == nil {
				if item.File.Scope == "drive" {
					s.fillFileUrl(item.File)
					filtered = append(filtered, item)
				}
			}
		}
	}

	if offset == 0 && len(filtered) < limit {
		existingFileIDs := make(map[uuid.UUID]struct{}, len(filtered))
		for _, item := range filtered {
			if item.File != nil {
				existingFileIDs[item.File.ID] = struct{}{}
			}
		}

		candidateLimit := 100
		if limit > 0 && limit*5 > candidateLimit {
			candidateLimit = limit * 5
		}

		candidates, err := s.repo.ListRecentlyUpdatedDriveFiles(ctx, candidateLimit)
		if err != nil {
			return nil, apperrors.Internal("list recent drive files failed")
		}

		for _, file := range candidates {
			if file == nil {
				continue
			}
			if _, exists := existingFileIDs[file.ID]; exists {
				continue
			}
			if _, err := s.canAccessFile(ctx, userID, file.ID, domain.RoleViewer); err != nil {
				continue
			}

			s.fillFileUrl(file)
			filtered = append(filtered, domain.RecentItem{
				LastAccessedAt: file.UpdatedAt,
				DriveItem: domain.DriveItem{
					Type: domain.ResourceFile,
					File: file,
				},
			})
			existingFileIDs[file.ID] = struct{}{}

			if len(filtered) >= limit {
				break
			}
		}
	}

	sort.SliceStable(filtered, func(i, j int) bool {
		return filtered[i].LastAccessedAt.After(filtered[j].LastAccessedAt)
	})

	if limit > 0 && len(filtered) > limit {
		filtered = filtered[:limit]
	}

	return filtered, nil
}

func (s *DriveService) ListDriveActivities(ctx context.Context, userID int64, folderID *uuid.UUID, limit, offset int) (*domain.DriveActivityListResponse, error) {
	accessProfile, err := s.repo.GetDriveActivityAccessProfile(ctx, userID)
	if err != nil {
		return nil, apperrors.InternalWithErr("drive activity access lookup failed", err)
	}

	ownerID := userID
	folderPathPrefix := ""
	if folderID != nil {
		folder, err := s.canAccessFolder(ctx, userID, *folderID, domain.RoleViewer)
		if err != nil {
			return nil, err
		}
		ownerID = folder.OwnerID
		folderPathPrefix, _ = s.buildFolderPathByParent(ctx, folder.ParentID, folder.Name)
	}

	var actorID *int64
	if accessProfile == nil || !accessProfile.CanViewAll {
		actorID = &userID
	}

	activities, totalCount, err := s.repo.ListDriveActivities(ctx, domain.DriveActivityListFilter{
		ViewerUserID:     userID,
		OwnerID:          ownerID,
		ActorID:          actorID,
		FolderID:         folderID,
		FolderPathPrefix: folderPathPrefix,
		Limit:            limit,
		Offset:           offset,
	})
	if err != nil {
		return nil, apperrors.InternalWithErr("list drive activities failed", err)
	}

	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	page := 1
	if limit > 0 {
		page = (offset / limit) + 1
	}
	totalPages := 0
	if totalCount > 0 {
		totalPages = (totalCount + limit - 1) / limit
	}

	return &domain.DriveActivityListResponse{
		Items:      activities,
		Page:       page,
		Limit:      limit,
		TotalCount: totalCount,
		TotalPages: totalPages,
		HasNext:    totalPages > 0 && page < totalPages,
		HasPrev:    page > 1,
	}, nil
}

func (s *DriveService) SearchAndFilter(ctx context.Context, userID int64, query, fileType string, limit, offset int, ownerUserID *int64, userFilter string) ([]domain.DriveItem, error) {
	// 1. Приводим к нижнему регистру, чтобы "Папка" и "папка" работали одинаково
	fileType = strings.ToLower(strings.TrimSpace(fileType))
	userFilter = strings.ToLower(strings.TrimSpace(userFilter))

	validType := ""
	switch fileType {
	case "folder", "папка", "бума":
		validType = "folder"

	case "image", "картинка", "фото", "сурет", "photo":
		validType = "image"

	case "video", "видео", "бейне":
		validType = "video"

	case "audio", "аудио", "дыбыс", "music", "музыка":
		validType = "audio"

	case "document", "документ", "құжат", "file", "файл":
		validType = "application"

	case "", "all", "все", "барлығы":
		validType = ""

	default:
		validType = ""
	}

	// Валидация userFilter
	validUserFilter := ""
	switch userFilter {
	case "owner", "владелец", "иесі":
		validUserFilter = "owner"
	case "participant", "участник", "қатысушы":
		validUserFilter = "participant"
	}

	params := repository.SearchParams{
		UserID:      userID,
		Query:       query,
		FileType:    validType,
		Limit:       limit,
		Offset:      offset,
		OwnerUserID: ownerUserID,
		UserFilter:  validUserFilter,
	}

	return s.repo.SearchItems(ctx, params)
}

// CreatePublicLink создает временную ссылку
func (s *DriveService) CreatePublicLink(ctx context.Context, userID int64, resourceType string, resourceID uuid.UUID, role string, ttl time.Duration) (*domain.DriveSharedLink, error) {
	if _, err := s.requireDriveManager(ctx, userID, domain.ResourceType(resourceType), resourceID); err != nil {
		return nil, err
	}
	normalized, err := normalizeDrivePermission(domain.PermissionRole(role))
	if err != nil {
		return nil, err
	}
	role = string(normalized)
	if ttl < 0 {
		return nil, apperrors.BadRequest("expires_in_sec must not be negative")
	}

	switch role {
	case domain.LinkRoleViewer, domain.LinkRoleOwner, domain.LinkRoleEditor:
	default:
		return nil, apperrors.BadRequest("invalid role")
	}

	var expiresAt *time.Time
	if ttl > 0 {
		t := time.Now().UTC().Add(ttl)
		expiresAt = &t
	}

	token, err := uuid.NewRandom()
	if err != nil {
		return nil, apperrors.Internal("failed to generate link token")
	}

	link := &domain.DriveSharedLink{
		Token:        token,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		CreatedBy:    userID,
		Role:         role,
		ExpiresAt:    expiresAt,
		CreatedAt:    time.Now().UTC(),
	}

	if err := s.repo.CreateSharedLink(ctx, link); err != nil {
		return nil, apperrors.Internal("failed to create link")
	}

	return link, nil
}

// AcceptPublicLink validates a link without creating permanent ACL grants.
// Every subsequent public operation must still present the active token.
func (s *DriveService) AcceptPublicLink(ctx context.Context, userID int64, token uuid.UUID) (*domain.PublicDriveItem, error) {
	return s.GetPublicItemByToken(ctx, token)
}

// GetPublicItemByToken получает данные по публичной ссылке
func (s *DriveService) GetPublicItemByToken(ctx context.Context, token uuid.UUID, target ...PublicLinkTarget) (*domain.PublicDriveItem, error) {
	link, err := s.resolvePublicLinkTarget(ctx, token, target...)
	if err != nil {
		return nil, err
	}

	resp := &domain.PublicDriveItem{
		Token:      link.Token,
		AccessRole: domain.LinkRoleViewer,
		ItemType:   link.ResourceType,
	}

	if link.ResourceType == "folder" {
		f, err := s.repo.GetFolderByID(ctx, link.ResourceID)
		if err != nil || f == nil || f.DeletedAt != nil {
			return nil, apperrors.NotFound("folder not found or deleted")
		}
		resp.Folder = f
		resp.Children, err = s.ListFolderChildren(ctx, link.CreatedBy, &f.ID)
		if err != nil {
			return nil, err
		}
		for _, child := range resp.Children {
			if child.File != nil {
				child.File.DownloadURL = s.publicLinkDownloadURL(token, child.File.ID)
			}
		}
	} else {
		f, err := s.repo.GetFileByID(ctx, link.ResourceID)
		if err != nil || f == nil || f.DeletedAt != nil {
			return nil, apperrors.NotFound("file not found or deleted")
		}
		s.fillFileUrl(f)
		f.DownloadURL = s.publicLinkDownloadURL(token, f.ID)

		resp.File = f
	}

	return resp, nil
}

// DownloadPublicFile возвращает stream файла по токену
func (s *DriveService) DownloadPublicFile(ctx context.Context, token uuid.UUID, target ...PublicLinkTarget) (io.ReadCloser, string, int64, string, error) {
	link, err := s.resolvePublicLinkTarget(ctx, token, target...)
	if err != nil {
		return nil, "", 0, "", err
	}
	if link.ResourceType != "file" {
		return nil, "", 0, "", apperrors.BadRequest("link is not for a file")
	}

	meta, err := s.repo.GetFileByID(ctx, link.ResourceID)
	if err != nil || meta == nil || meta.DeletedAt != nil {
		return nil, "", 0, "", apperrors.NotFound("file not found")
	}

	reader, _, size, err := s.fileSvc.DownloadFile(ctx, meta.ObjectName)
	if err != nil {
		return nil, "", 0, "", err
	}

	if err := s.auditDriveDownload(ctx, 0, meta, meta.Version, true); err != nil {
		reader.Close()
		return nil, "", 0, "", err
	}

	// Используем MIME type из БД, а не из MinIO
	// Используем MIME type из БД, а не из MinIO
	mimeType := meta.MimeType

	// Если мы видим generic zip, но по расширению знаем что это pptx/docx/xlsx, исправляем.
	if mimeType == "" || mimeType == "application/zip" || mimeType == "application/octet-stream" {
		correctedType := s.fileSvc.GetContentTypeByFilename(meta.RealName)
		if correctedType != "application/octet-stream" {
			mimeType = correctedType
		} else if mimeType == "" {
			mimeType = "application/octet-stream"
		}
	}

	return reader, mimeType, size, meta.RealName, nil
}

func (s *DriveService) CopyItem(ctx context.Context, userID int64, resourceType domain.ResourceType, resourceID uuid.UUID, targetParentID *uuid.UUID) (any, error) {
	if targetParentID != nil {
		if _, err := s.canWorkInFolder(ctx, userID, *targetParentID, domain.RoleEditor); err != nil {
			return nil, err
		}
	}

	if resourceType == domain.ResourceFolder {
		if err := s.validateFolderDestination(ctx, resourceID, targetParentID); err != nil {
			return nil, err
		}
	}

	if resourceType == domain.ResourceFolder {
		sourceFolder, err := s.canAccessFolder(ctx, userID, resourceID, domain.RoleViewer)
		if err != nil {
			return nil, err
		}
		exists, err := s.repo.FolderNameExistsInParent(ctx, userID, targetParentID, sourceFolder.Name, nil)
		if err != nil {
			return nil, apperrors.Internal("folder duplicate check failed")
		}
		if exists {
			return nil, apperrors.Conflict("folder with the same name already exists in the target location")
		}
		sourcePath, _ := s.buildFolderPathByParent(ctx, sourceFolder.ParentID, sourceFolder.Name)

		newFolder, err := s.repo.CopyFolder(ctx, resourceID, targetParentID, userID)
		if err != nil {
			return nil, apperrors.InternalWithErr("failed to copy folder", err)
		}

		if s.driveSearch != nil {
			go func(newFldID uuid.UUID) {
				asyncCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				defer cancel()
				fileIDs, err := s.repo.ListFileIDsInFolderTree(asyncCtx, newFldID)
				if err == nil {
					for _, fid := range fileIDs {
						s.syncDriveFileIndex(fid)
					}
				}
			}(newFolder.ID)
		}

		s.recordActivity(userID, domain.ResourceFolder, newFolder.ID)
		if copiedPath, err := s.buildFolderPathByParent(ctx, targetParentID, newFolder.Name); err == nil {
			s.logDriveActivity(ctx, &domain.DriveActivity{
				Action:       domain.DriveActivityActionCopied,
				ResourceType: domain.ResourceFolder,
				ResourceID:   newFolder.ID,
				ResourceName: newFolder.Name,
				OwnerID:      newFolder.OwnerID,
				ActorID:      userID,
				FolderID:     cloneUUIDPtr(targetParentID),
				ToFolderID:   cloneUUIDPtr(targetParentID),
				ResourcePath: copiedPath,
				FromPath:     sourcePath,
				ToPath:       copiedPath,
				Metadata: rawJSONMap(map[string]any{
					"source_resource_id": resourceID.String(),
				}),
			})
		}
		return newFolder, nil
	} else if resourceType == domain.ResourceFile {
		sourceFile, err := s.canAccessFile(ctx, userID, resourceID, domain.RoleViewer)
		if err != nil {
			return nil, err
		}
		sourcePath, _ := s.buildFilePath(ctx, sourceFile.FolderID, sourceFile.RealName)

		newFile, err := s.repo.CopyFile(ctx, resourceID, targetParentID, userID)
		if err != nil {
			return nil, apperrors.InternalWithErr("failed to copy file", err)
		}

		s.fillFileUrl(newFile)
		if s.driveSearch != nil {
			s.syncDriveFileIndex(newFile.ID)
		}

		s.recordActivity(userID, domain.ResourceFile, newFile.ID)
		if copiedPath, err := s.buildFilePath(ctx, targetParentID, newFile.RealName); err == nil {
			s.logDriveActivity(ctx, &domain.DriveActivity{
				Action:       domain.DriveActivityActionCopied,
				ResourceType: domain.ResourceFile,
				ResourceID:   newFile.ID,
				ResourceName: newFile.RealName,
				OwnerID:      newFile.OwnerID,
				ActorID:      userID,
				FolderID:     cloneUUIDPtr(targetParentID),
				ToFolderID:   cloneUUIDPtr(targetParentID),
				ResourcePath: copiedPath,
				FromPath:     sourcePath,
				ToPath:       copiedPath,
				Metadata: rawJSONMap(map[string]any{
					"source_resource_id": resourceID.String(),
				}),
			})
		}
		return newFile, nil
	}

	return nil, apperrors.BadRequest("invalid resource type")
}

func (s *DriveService) MoveItem(ctx context.Context, userID int64, resourceType domain.ResourceType, resourceID uuid.UUID, targetParentID *uuid.UUID) (any, error) {
	if targetParentID != nil {
		if _, err := s.canWorkInFolder(ctx, userID, *targetParentID, domain.RoleEditor); err != nil {
			return nil, err
		}
	}

	switch resourceType {
	case domain.ResourceFolder:
		folder, err := s.canAccessFolder(ctx, userID, resourceID, domain.RoleEditor)
		if err != nil {
			return nil, err
		}
		originalPath, _ := s.buildFolderPathByParent(ctx, folder.ParentID, folder.Name)
		if folder.ParentID == nil && targetParentID == nil {
			return folder, nil
		}
		if folder.ParentID != nil && targetParentID != nil && *folder.ParentID == *targetParentID {
			return folder, nil
		}
		if err := s.validateFolderDestination(ctx, resourceID, targetParentID); err != nil {
			return nil, err
		}

		if exists, err := s.repo.FolderNameExistsInParent(ctx, folder.OwnerID, targetParentID, folder.Name, &resourceID); err != nil {
			return nil, apperrors.InternalWithErr("folder duplicate check failed", err)
		} else if exists {
			return nil, apperrors.Conflict("folder with the same name already exists in the target location").
				WithUserMessage("В этой папке уже есть папка с таким названием.")
		}
		if err := s.repo.MoveFolder(ctx, resourceID, targetParentID); err != nil {
			if errors.Is(err, repository.ErrFolderCycle) {
				return nil, apperrors.BadRequest(err.Error())
			}
			if err.Error() == "folder not found" {
				return nil, apperrors.NotFound("folder not found")
			}
			return nil, apperrors.Internal("move folder failed")
		}
		s.recordActivity(userID, domain.ResourceFolder, resourceID)
		updated, err := s.repo.GetFolderByID(ctx, resourceID)
		if err != nil || updated == nil || updated.DeletedAt != nil {
			return nil, apperrors.NotFound("folder not found")
		}
		if updatedPath, err := s.buildFolderPathByParent(ctx, updated.ParentID, updated.Name); err == nil {
			s.logDriveActivity(ctx, &domain.DriveActivity{
				Action:       domain.DriveActivityActionMoved,
				ResourceType: domain.ResourceFolder,
				ResourceID:   resourceID,
				ResourceName: updated.Name,
				OwnerID:      updated.OwnerID,
				ActorID:      userID,
				FolderID:     cloneUUIDPtr(updated.ParentID),
				FromFolderID: cloneUUIDPtr(folder.ParentID),
				ToFolderID:   cloneUUIDPtr(updated.ParentID),
				ResourcePath: updatedPath,
				FromPath:     originalPath,
				ToPath:       updatedPath,
			})
		}
		return updated, nil

	case domain.ResourceFile:
		file, err := s.canAccessFile(ctx, userID, resourceID, domain.RoleEditor)
		if err != nil {
			return nil, err
		}
		originalPath, _ := s.buildFilePath(ctx, file.FolderID, file.RealName)
		if file.FolderID == nil && targetParentID == nil {
			s.fillFileUrl(file)
			return file, nil
		}
		if file.FolderID != nil && targetParentID != nil && *file.FolderID == *targetParentID {
			s.fillFileUrl(file)
			return file, nil
		}
		if err := s.repo.MoveFile(ctx, resourceID, targetParentID); err != nil {
			if err.Error() == "file not found" {
				return nil, apperrors.NotFound("file not found")
			}
			return nil, apperrors.Internal("move file failed")
		}
		s.recordActivity(userID, domain.ResourceFile, resourceID)
		s.syncDriveFileIndex(resourceID)
		updated, err := s.repo.GetFileByID(ctx, resourceID)
		if err != nil || updated == nil || updated.DeletedAt != nil {
			return nil, apperrors.NotFound("file not found")
		}
		s.fillFileUrl(updated)
		if updatedPath, err := s.buildFilePath(ctx, updated.FolderID, updated.RealName); err == nil {
			s.logDriveActivity(ctx, &domain.DriveActivity{
				Action:       domain.DriveActivityActionMoved,
				ResourceType: domain.ResourceFile,
				ResourceID:   resourceID,
				ResourceName: updated.RealName,
				OwnerID:      updated.OwnerID,
				ActorID:      userID,
				FolderID:     cloneUUIDPtr(updated.FolderID),
				FromFolderID: cloneUUIDPtr(file.FolderID),
				ToFolderID:   cloneUUIDPtr(updated.FolderID),
				ResourcePath: updatedPath,
				FromPath:     originalPath,
				ToPath:       updatedPath,
			})
		}
		return updated, nil
	default:
		return nil, apperrors.BadRequest("invalid resource type")
	}
}

// Client disconnects must not prevent removal of objects with no saved metadata.
func (s *DriveService) rollbackDriveUpload(ctx context.Context, objectName string) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	return s.fileSvc.DeleteFile(cleanupCtx, objectName)
}

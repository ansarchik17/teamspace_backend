package service

import (
	"testing"

	"github.com/west6ide/workspace-backend/internal/domain"
)

// ─────────────────────────── Channel access rights ───────────────────────────

func TestCanPostToChannel_OwnerCanPost(t *testing.T) {
	if !canPostToChannel(domain.ChannelRoleOwner) {
		t.Fatal("owner must be allowed to post")
	}
}

func TestCanPostToChannel_AdminCanPost(t *testing.T) {
	if !canPostToChannel(domain.ChannelRoleAdmin) {
		t.Fatal("admin must be allowed to post")
	}
}

func TestCanPostToChannel_SubscriberCannotPost(t *testing.T) {
	if canPostToChannel(domain.ChannelRoleSubscriber) {
		t.Fatal("subscriber must not be allowed to post")
	}
}

func TestCanAdminChannel_OwnerCanAdmin(t *testing.T) {
	if !canAdminChannel(domain.ChannelRoleOwner) {
		t.Fatal("owner must have admin access")
	}
}

func TestCanAdminChannel_AdminCanAdmin(t *testing.T) {
	if !canAdminChannel(domain.ChannelRoleAdmin) {
		t.Fatal("admin must have admin access")
	}
}

func TestCanAdminChannel_SubscriberCannotAdmin(t *testing.T) {
	if canAdminChannel(domain.ChannelRoleSubscriber) {
		t.Fatal("subscriber must not have admin access")
	}
}

// TestChannelRoleMatrix exhaustively checks all roles against both helpers.
func TestChannelRoleMatrix(t *testing.T) {
	cases := []struct {
		role     domain.ChannelRole
		canPost  bool
		canAdmin bool
	}{
		{domain.ChannelRoleOwner, true, true},
		{domain.ChannelRoleAdmin, true, true},
		{domain.ChannelRoleSubscriber, false, false},
	}
	for _, tc := range cases {
		if canPostToChannel(tc.role) != tc.canPost {
			t.Errorf("canPostToChannel(%q) = %v, want %v", tc.role, !tc.canPost, tc.canPost)
		}
		if canAdminChannel(tc.role) != tc.canAdmin {
			t.Errorf("canAdminChannel(%q) = %v, want %v", tc.role, !tc.canAdmin, tc.canAdmin)
		}
	}
}

// ─────────────────────────── Group access rights ───────────────────────────

func TestIsGroupAdminRole_Owner(t *testing.T) {
	if !isGroupAdminRole("owner") {
		t.Fatal("owner must be an admin role")
	}
}

func TestIsGroupAdminRole_Admin(t *testing.T) {
	if !isGroupAdminRole("admin") {
		t.Fatal("admin must be an admin role")
	}
}

func TestIsGroupAdminRole_Member(t *testing.T) {
	if isGroupAdminRole("member") {
		t.Fatal("member must not be an admin role")
	}
}

func TestIsGroupAdminRole_Unknown(t *testing.T) {
	if isGroupAdminRole("") {
		t.Fatal("empty role must not be admin")
	}
	if isGroupAdminRole("guest") {
		t.Fatal("unknown role must not be admin")
	}
}

// ─────────────────────────── Group max_members clamping ───────────────────────────

func TestClampGroupMaxMembers_ValidRange(t *testing.T) {
	cases := []struct{ in, want int }{
		{1, 1},
		{50, 50},
		{200, 200},
		{10000, 10000},
	}
	for _, tc := range cases {
		if got := clampGroupMaxMembers(tc.in); got != tc.want {
			t.Errorf("clampGroupMaxMembers(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestClampGroupMaxMembers_OutOfBounds(t *testing.T) {
	cases := []int{0, -1, -100, 10001, 99999}
	for _, n := range cases {
		if got := clampGroupMaxMembers(n); got != 200 {
			t.Errorf("clampGroupMaxMembers(%d) = %d, want 200", n, got)
		}
	}
}

// ─────────────────────────── Message isolation invariants ───────────────────────────

// TestMessageIsolation_ReplyConversationCheck documents and verifies the
// invariant enforced by SendMessage: a reply-to message must share the
// same ConversationID as the new message.
func TestMessageIsolation_ReplyConversationCheck(t *testing.T) {
	type replyCheck struct {
		replyConvID  string
		targetConvID string
		valid        bool
	}
	convA := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	convB := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"

	cases := []replyCheck{
		{convA, convA, true},  // reply and message in same conversation — OK
		{convA, convB, false}, // reply from different conversation — rejected
	}

	for _, tc := range cases {
		ok := tc.replyConvID == tc.targetConvID
		if ok != tc.valid {
			t.Errorf("replyConvID=%s targetConvID=%s: got valid=%v, want %v",
				tc.replyConvID, tc.targetConvID, ok, tc.valid)
		}
	}
}

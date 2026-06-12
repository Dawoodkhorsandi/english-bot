package app

import "testing"

func TestToggleIcon(t *testing.T) {
	if toggleIcon(true) != "✅" {
		t.Errorf("toggleIcon(true) = %q, want ✅", toggleIcon(true))
	}
	if toggleIcon(false) != "❌" {
		t.Errorf("toggleIcon(false) = %q, want ❌", toggleIcon(false))
	}
}

func TestAdminListUsersPagination(t *testing.T) {
	s := testStoreHelper(t)
	for _, id := range []int64{101, 102, 103, 104, 105} {
		if _, err := s.AddSubscriber(id); err != nil {
			t.Fatalf("AddSubscriber(%d): %v", id, err)
		}
	}

	page0, total, err := s.AdminListUsers(0, 2)
	if err != nil {
		t.Fatalf("AdminListUsers page 0: %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}
	if len(page0) != 2 {
		t.Errorf("page size = %d, want 2", len(page0))
	}

	// Last page holds the remainder.
	page2, _, err := s.AdminListUsers(2, 2)
	if err != nil {
		t.Fatalf("AdminListUsers page 2: %v", err)
	}
	if len(page2) != 1 {
		t.Errorf("last page size = %d, want 1", len(page2))
	}
}

func TestAdminUserDetail(t *testing.T) {
	s := testStoreHelper(t)
	if _, err := s.AddSubscriber(777); err != nil {
		t.Fatalf("AddSubscriber: %v", err)
	}
	if err := s.SetLevel(777, "advanced"); err != nil {
		t.Fatalf("SetLevel: %v", err)
	}

	d, err := s.AdminUserDetail(777)
	if err != nil {
		t.Fatalf("AdminUserDetail: %v", err)
	}
	if d.ChatID != 777 {
		t.Errorf("ChatID = %d, want 777", d.ChatID)
	}
	if d.Level != "advanced" {
		t.Errorf("Level = %q, want advanced", d.Level)
	}
	// A brand-new user has no delivered content yet.
	if d.Words != 0 || d.Verbs != 0 {
		t.Errorf("new user counts: words=%d verbs=%d, want 0/0", d.Words, d.Verbs)
	}
}

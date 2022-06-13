package database

import (
	"context"
	"testing"

	"akvorado/common/helpers"
	"akvorado/common/reporter"
)

func TestSavedFilter(t *testing.T) {
	r := reporter.NewMock(t)
	c := NewMock(t, r)

	// Create
	if err := c.CreateSavedFilter(context.Background(), SavedFilter{
		ID:          17,
		User:        "marty",
		Description: "marty's filter",
		Content:     "SrcAS = 12322",
	}); err != nil {
		t.Fatalf("CreateSavedFilter() error:\n%+v", err)
	}
	if err := c.CreateSavedFilter(context.Background(), SavedFilter{
		User:        "judith",
		Folder:      "something",
		Description: "judith's filter",
		Content:     "InIfBoundary = external",
	}); err != nil {
		t.Fatalf("CreateSavedFilter() error:\n%+v", err)
	}
	if err := c.CreateSavedFilter(context.Background(), SavedFilter{
		User:        "marty",
		Folder:      "else",
		Description: "marty's second filter",
		Content:     "InIfBoundary = internal",
	}); err != nil {
		t.Fatalf("CreateSavedFilter() error:\n%+v", err)
	}

	// List
	got, err := c.ListSavedFilters(context.Background(), "marty")
	if err != nil {
		t.Fatalf("ListSavedFilters() error:\n%+v", err)
	}
	if diff := helpers.Diff(got, []SavedFilter{
		{
			ID:          1,
			User:        "marty",
			Description: "marty's filter",
			Content:     "SrcAS = 12322",
		}, {
			ID:          3,
			User:        "marty",
			Folder:      "else",
			Description: "marty's second filter",
			Content:     "InIfBoundary = internal",
		},
	}); diff != "" {
		t.Fatalf("ListSavedFilters() (-got, +want):\n%s", diff)
	}

	// Delete
	if err := c.DeleteSavedFilter(context.Background(), SavedFilter{ID: 1}); err != nil {
		t.Fatalf("DeleteSavedFilter() error:\n%+v", err)
	}
	got, _ = c.ListSavedFilters(context.Background(), "marty")
	if diff := helpers.Diff(got, []SavedFilter{
		{
			ID:          3,
			User:        "marty",
			Folder:      "else",
			Description: "marty's second filter",
			Content:     "InIfBoundary = internal",
		},
	}); diff != "" {
		t.Fatalf("ListSavedFilters() (-got, +want):\n%s", diff)
	}
	if err := c.DeleteSavedFilter(context.Background(), SavedFilter{ID: 1}); err == nil {
		t.Fatal("DeleteSavedFilter() no error")
	}
}

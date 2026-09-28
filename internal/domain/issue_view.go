package domain

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

type IssueViewDefinition struct {
	View    string                 `json:"view"`
	Filters *IssueFilterExpression `json:"filters"`
}

func ValidIssueView(view string) bool {
	switch view {
	case "all", "open", "regressed", "critical", "assigned", "resolved", "suppressed":
		return true
	default:
		return false
	}
}

type IssueSavedView struct {
	ID           uuid.UUID           `json:"id"`
	Name         string              `json:"name"`
	Visibility   string              `json:"visibility"`
	Revision     int                 `json:"revision"`
	Definition   IssueViewDefinition `json:"definition"`
	OwnerSubject string              `json:"owner_subject"`
	CanManage    bool                `json:"can_manage"`
	CreatedAt    time.Time           `json:"created_at"`
	UpdatedAt    time.Time           `json:"updated_at"`
}

type IssueSavedViewPage struct {
	Views              []IssueSavedView `json:"views"`
	CanCreateWorkspace bool             `json:"can_create_workspace"`
}

type IssueSavedViewInput struct {
	Name       string              `json:"name"`
	Visibility string              `json:"visibility"`
	Revision   int                 `json:"revision"`
	Definition IssueViewDefinition `json:"definition"`
}

func (input IssueSavedViewInput) Valid() bool {
	return utf8.RuneCountInString(strings.TrimSpace(input.Name)) >= 1 && utf8.RuneCountInString(strings.TrimSpace(input.Name)) <= 80 && !strings.ContainsRune(input.Name, '\x00') && (input.Visibility == "personal" || input.Visibility == "workspace") && ValidIssueView(input.Definition.View) && input.Definition.Filters != nil && input.Definition.Filters.Valid()
}

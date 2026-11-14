package domain

import "time"

type SearchResultItem struct {
	Type         string         `json:"type"`
	ID           string         `json:"id"`
	Title        string         `json:"title"`
	Description  string         `json:"description,omitempty"`
	MatchedField string         `json:"matched_field,omitempty"`
	CreatedAt    *time.Time     `json:"created_at,omitempty"`
	UpdatedAt    *time.Time     `json:"updated_at,omitempty"`
	Payload      map[string]any `json:"payload,omitempty"`
}

type SearchAllData struct {
	Query string             `json:"query"`
	Total int                `json:"total"`
	Items []SearchResultItem `json:"items"`
}

type SearchResult struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

type SearchAllResponse struct {
	Success       bool            `json:"success"`
	Data          SearchAllData   `json:"data"`
	Users         []*UserResponse `json:"users"`
	Tasks         []*Task         `json:"tasks"`
	Organizations []SearchResult  `json:"organizations"`
	// Messages is retained as an empty array for existing search clients.
	Messages  []any       `json:"messages"`
	Events    []Event     `json:"events"`
	Meetings  []Event     `json:"meetings"`
	Documents []DriveItem `json:"documents"`
	Notes     []Note      `json:"notes"`
}

type GlobalSearchItem = SearchResultItem
type GlobalSearchData = SearchAllData
type GlobalSearchResponse = SearchAllResponse

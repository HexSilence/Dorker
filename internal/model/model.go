package model

import "time"

type Result struct {
	URL              string    `json:"url"`
	NormalizedURL    string    `json:"normalized_url"`
	Domain           string    `json:"domain"`
	Title            string    `json:"title,omitempty"`
	Snippet          string    `json:"snippet,omitempty"`
	Provider         string    `json:"provider"`
	ProviderInstance string    `json:"provider_instance,omitempty"`
	SourceEngines    []string  `json:"source_engines,omitempty"`
	OriginalQuery    string    `json:"original_query"`
	QueryVariant     string    `json:"query_variant"`
	Rank             int       `json:"rank"`
	Page             string    `json:"page,omitempty"`
	DiscoveredAt     time.Time `json:"discovered_at"`
}

type QueryVariant struct {
	Original string
	Variant  string
	Ordinal  int
}

type Job struct {
	ID        string
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
	Queries   int
	Unique    int64
}

type Stats struct {
	Raw, Accepted, Rejected, UnknownKept, Unique, Duplicates, Malformed, Errors int64
	QueriesDone, QueriesTotal                                                   int64
	ByProvider                                                                  map[string]int64
	BySourceEngine                                                              map[string]int64
	RejectionReasons                                                            map[string]int64
	UpstreamDegradation                                                         []UpstreamEngineStat
	ProviderFailures                                                            []ProviderFailure
}
type ProviderFailure struct {
	Provider, Kind, Message string
	Count                   int64
}
type UpstreamEngineStat struct {
	Provider, ProviderInstance, Engine, Classification, LastMessage string
	Count                                                           int64
	LastSeen                                                        time.Time
}

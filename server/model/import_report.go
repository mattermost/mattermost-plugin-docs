// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package model

// ImportReportVersion is the schema version of the downloadable JSON report.
const ImportReportVersion = 1

// ImportEntityRef identifies the entity an issue or result refers to.
type ImportEntityRef struct {
	Type       string `json:"type"`
	ExternalId string `json:"external_id,omitempty"`
	LocalId    string `json:"local_id,omitempty"`
	Title      string `json:"title,omitempty"`
}

// ImportIssue is one structured finding in a report. Codes are stable and documented.
type ImportIssue struct {
	Stage       string           `json:"stage"`
	Severity    string           `json:"severity"`
	Code        string           `json:"code"`
	Entity      *ImportEntityRef `json:"entity,omitempty"`
	Message     string           `json:"message"`
	Remediation string           `json:"remediation,omitempty"`
	Details     map[string]any   `json:"details,omitempty"`
}

// ImportResult is one entity-level outcome in a report.
type ImportResult struct {
	Stage         string          `json:"stage"`
	Entity        ImportEntityRef `json:"entity"`
	PlannedAction string          `json:"planned_action,omitempty"`
	ActualAction  string          `json:"actual_action,omitempty"`
	Outcome       string          `json:"outcome"`
	Details       map[string]any  `json:"details,omitempty"`
}

// ImportReportSource is the report's source-identity block.
type ImportReportSource struct {
	OrganizationId string `json:"organization_id"`
	SpaceKey       string `json:"space_key"`
	SpaceName      string `json:"space_name"`
	ImportSourceId string `json:"import_source_id,omitempty"`
}

// ImportReportTarget is the report's target block.
type ImportReportTarget struct {
	Kind    string `json:"kind"`
	TeamId  string `json:"team_id"`
	SpaceId string `json:"space_id,omitempty"`
	Existed bool   `json:"existed"`
}

// Fidelity string constants. Every report advertises exactly these values so no client can mistake
// a page-only import for a full-fidelity one.
const (
	FidelityScopePagesOnly                = "pages_only"
	FidelityCountedNotImported            = "counted_not_imported"
	FidelityRestrictedWidened             = "imported_with_widened_access"
	FidelityRestrictedReportedNotImported = "reported_not_imported"
)

// ImportFidelity is the mandatory fidelity disclosure embedded in every report and job view.
type ImportFidelity struct {
	Scope                         string `json:"scope"`
	Comments                      string `json:"comments"`
	Attachments                   string `json:"attachments"`
	RestrictedEmittedPages        string `json:"restricted_emitted_pages"`
	RestrictedManifestOnlyEntries string `json:"restricted_manifest_only_entries"`
	FullFidelity                  bool   `json:"full_fidelity"`
}

// NewImportFidelity returns the fixed fidelity disclosure for this release.
func NewImportFidelity() ImportFidelity {
	return ImportFidelity{
		Scope:                         FidelityScopePagesOnly,
		Comments:                      FidelityCountedNotImported,
		Attachments:                   FidelityCountedNotImported,
		RestrictedEmittedPages:        FidelityRestrictedWidened,
		RestrictedManifestOnlyEntries: FidelityRestrictedReportedNotImported,
		FullFidelity:                  false,
	}
}

// ImportReportCounts aggregates action and entity counts for a report.
type ImportReportCounts struct {
	Pages            int            `json:"pages"`
	Comments         int            `json:"comments"`
	Attachments      int            `json:"attachments"`
	Actions          map[string]int `json:"actions"`
	Authors          map[string]int `json:"authors,omitempty"`
	Links            map[string]int `json:"links,omitempty"`
	IssuesBySeverity map[string]int `json:"issues_by_severity,omitempty"`
}

// ImportReportSummary is the compact report projection embedded in ImportJobView (no per-entity
// results or issues; those stream from the report/issues endpoints).
type ImportReportSummary struct {
	Stage       string             `json:"stage"`
	GeneratedAt int64              `json:"generated_at"`
	Fidelity    ImportFidelity     `json:"fidelity"`
	Counts      ImportReportCounts `json:"counts"`
}

// ImportReport is the full downloadable report. Results and Issues stream from persisted rows so
// the endpoint need not hold them all in memory.
type ImportReport struct {
	ReportVersion int                `json:"report_version"`
	Stage         string             `json:"stage"`
	JobId         string             `json:"job_id"`
	GeneratedAt   int64              `json:"generated_at"`
	Source        ImportReportSource `json:"source"`
	Target        ImportReportTarget `json:"target"`
	Fidelity      ImportFidelity     `json:"fidelity"`
	Counts        ImportReportCounts `json:"counts"`
	Results       []ImportResult     `json:"results"`
	Issues        []ImportIssue      `json:"issues"`
}

// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	sq "github.com/mattermost/squirrel"
	"github.com/pkg/errors"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// Report reads are keyset-paged on Ordinal rather than offset-paged, unlike the interactive list endpoints.
//
// A downloaded report is one logical read spanning potentially thousands of rows, so it must not skip or
// duplicate any of them: OFFSET restarts the scan each time and shifts under concurrent writes, while "give me
// what comes after this ordinal" is stable whatever else happens. Ordinal is unique per (job, stage), which is
// what makes it a usable cursor.

// GetImportResultsAfter returns one page of a job's results for a stage, in ordinal order, starting after
// afterOrdinal. Pass -1 to start from the beginning.
func (s *Store) GetImportResultsAfter(
	jobID string,
	stage model.ImportIssueStage,
	afterOrdinal, limit int,
) ([]*model.ImportResultRecord, error) {
	if jobID == "" {
		return nil, &ErrInvalidInput{Entity: "ImportResult", Field: "jobID", Value: jobID}
	}
	if !stage.IsValid() {
		return nil, &ErrInvalidInput{Entity: "ImportResult", Field: "stage", Value: string(stage)}
	}
	if err := requirePositiveLimit("ImportResult", limit); err != nil {
		return nil, err
	}
	builder := s.getQueryBuilder().
		Select(importResultColumns...).
		From("DOCS_ImportResult").
		Where(sq.Eq{"JobId": jobID, "Stage": string(stage)}).
		Where(sq.Gt{"Ordinal": afterOrdinal}).
		OrderBy("Ordinal ASC")
	builder = applyLimitOffset(builder, 0, limit)

	records := []*model.ImportResultRecord{}
	if err := s.selectBuilder(s.db, &records, builder); err != nil {
		return nil, errors.Wrap(err, "unable_to_get_import_results")
	}
	return records, nil
}

// GetImportIssuesAfter returns one page of a job's issues for a stage, in ordinal order, starting after
// afterOrdinal. Pass -1 to start from the beginning.
func (s *Store) GetImportIssuesAfter(
	jobID string,
	stage model.ImportIssueStage,
	afterOrdinal, limit int,
) ([]*model.ImportIssueRecord, error) {
	if jobID == "" {
		return nil, &ErrInvalidInput{Entity: "ImportIssue", Field: "jobID", Value: jobID}
	}
	if !stage.IsValid() {
		return nil, &ErrInvalidInput{Entity: "ImportIssue", Field: "stage", Value: string(stage)}
	}
	if err := requirePositiveLimit("ImportIssue", limit); err != nil {
		return nil, err
	}
	builder := s.getQueryBuilder().
		Select(importIssueColumns...).
		From("DOCS_ImportIssue").
		Where(sq.Eq{"JobId": jobID, "Stage": string(stage)}).
		Where(sq.Gt{"Ordinal": afterOrdinal}).
		OrderBy("Ordinal ASC")
	builder = applyLimitOffset(builder, 0, limit)

	records := []*model.ImportIssueRecord{}
	if err := s.selectBuilder(s.db, &records, builder); err != nil {
		return nil, errors.Wrap(err, "unable_to_get_import_issues")
	}
	return records, nil
}

// CountImportIssuesBySeverity counts a job's issues by severity across the given stages, for the report's
// summary block. Counting in SQL is the point: the report streams its issue rows, so it cannot tally them on
// the way past without buffering the whole set.
func (s *Store) CountImportIssuesBySeverity(jobID string, stages []model.ImportIssueStage) (map[string]int, error) {
	if jobID == "" {
		return nil, &ErrInvalidInput{Entity: "ImportIssue", Field: "jobID", Value: jobID}
	}
	names := make([]string, 0, len(stages))
	for _, stage := range stages {
		if !stage.IsValid() {
			return nil, &ErrInvalidInput{Entity: "ImportIssue", Field: "stage", Value: string(stage)}
		}
		names = append(names, string(stage))
	}

	var rows []struct {
		Severity string
		Cnt      int
	}
	builder := s.getQueryBuilder().
		Select("Severity", "COUNT(*) AS cnt").
		From("DOCS_ImportIssue").
		Where(sq.Eq{"JobId": jobID, "Stage": names}).
		GroupBy("Severity")
	if err := s.selectBuilder(s.db, &rows, builder); err != nil {
		return nil, errors.Wrap(err, "unable_to_count_import_issues_by_severity")
	}

	counts := map[string]int{}
	for _, row := range rows {
		counts[row.Severity] = row.Cnt
	}
	return counts, nil
}

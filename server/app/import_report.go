// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// importReportBatch is how many result or issue rows one report read fetches. It bounds resident rows rather
// than total rows: a five-thousand-page job's report streams in batches of this size.
const importReportBatch = 200

// ImportReportStageFinal is the public name for the execution-stage report.
//
// A reader asks for the "final" report, not the "execution" one: the internal stage name is a pipeline detail,
// and the two reports are distinguished for a user by *when* they describe the import rather than by which
// worker phase produced their rows.
const ImportReportStageFinal = "final"

// importReportStageOf maps a requested stage name onto the result stage it selects. Only the two documented
// names are accepted: "inspection" is a stage rows carry, not a report a caller may ask for, and silently
// treating an unknown name as one of the two would hand back a report the caller did not request.
func importReportStageOf(stage string) (model.ImportIssueStage, bool) {
	switch stage {
	case string(model.ImportStagePreflight):
		return model.ImportStagePreflight, true
	case ImportReportStageFinal:
		return model.ImportStageExecution, true
	default:
		return "", false
	}
}

// ImportReportStream is an authorized, ready-to-write report.
//
// Preparing and writing are deliberately two steps. Every reason to refuse — the job is not the caller's, the
// caller has lost access, the stage is not ready — must be decided *before* the first byte, because an HTTP
// status cannot be taken back once the body has started. Once a stream exists, writing it can only fail for
// reasons the client can no longer be told about.
type ImportReportStream struct {
	svc   *Service
	job   *model.ImportJob
	stage model.ImportIssueStage
	// resultStage is the result rows this report presents as its outcomes, and issueStages every issue stage it
	// includes.
	resultStage model.ImportIssueStage
	issueStages []model.ImportIssueStage
	counts      model.ImportReportCounts
	generatedAt int64
}

// PrepareImportReport authorizes a report download and resolves what it will contain.
//
// Visibility matches the rest of the import read surface and then some: the job's own actor only, and nothing
// at all once they have lost access to the target. A report names pages, titles, and local ids inside a Space,
// so losing access to that Space hides it entirely — reported as absent rather than forbidden, so the endpoint
// cannot be used to confirm that someone else's import exists.
func (s *Service) PrepareImportReport(jobID, actorID, stage string) (*ImportReportStream, *mmmodel.AppError) {
	job, appErr := s.getOwnImportJob(jobID, actorID)
	if appErr != nil {
		return nil, appErr
	}
	entitled, appErr := s.actorStillEntitled(job, actorID)
	if appErr != nil {
		return nil, appErr
	}
	if !entitled {
		return nil, mmmodel.NewAppError("PrepareImportReport", "app.store.not_found.app_error", nil, "", http.StatusNotFound)
	}

	requested, ok := importReportStageOf(stage)
	if !ok {
		return nil, mmmodel.NewAppError("PrepareImportReport", "app.import.report.invalid_stage.app_error", nil, "", http.StatusBadRequest)
	}

	stream := &ImportReportStream{svc: s, job: job, stage: requested}
	switch requested {
	case model.ImportStagePreflight:
		if job.PreflightRevision == "" {
			// Nothing has been reviewed yet, so there is no plan to hand out. 409 rather than 404: the job is
			// real and this report will exist, just not yet.
			return nil, mmmodel.NewAppError("PrepareImportReport", "app.import.report.not_ready.app_error", nil, "", http.StatusConflict)
		}
		stream.resultStage = model.ImportStagePreflight
		// Inspection findings describe the bundle rather than the plan, so both reports carry them: they are
		// immutable and are as much a part of understanding a plan as of understanding an outcome.
		stream.issueStages = []model.ImportIssueStage{model.ImportStageInspection, model.ImportStagePreflight}
		stream.counts = importPreflightReportSummary(job).Counts
		stream.generatedAt = job.UpdateAt
	default:
		if !job.State.IsTerminal() {
			return nil, mmmodel.NewAppError("PrepareImportReport", "app.import.report.not_ready.app_error", nil, "", http.StatusConflict)
		}
		// Execution results only. A finished import must not present its historical plan as what happened —
		// those classifications remain available from the preflight report, labelled as the plan they were.
		stream.resultStage = model.ImportStageExecution
		stream.issueStages = []model.ImportIssueStage{model.ImportStageInspection, model.ImportStageExecution}
		stream.counts = importFinalReportSummary(job).Counts
		stream.generatedAt = job.FinishedAt
	}

	severities, err := s.store.CountImportIssuesBySeverity(job.Id, stream.issueStages)
	if err != nil {
		return nil, storeAppError("PrepareImportReport", err)
	}
	if len(severities) > 0 {
		stream.counts.IssuesBySeverity = severities
	}
	return stream, nil
}

// Filename is the download name for this report.
func (r *ImportReportStream) Filename() string {
	return "docs-import-" + r.job.Id + "-" + r.stageLabel() + ".json"
}

// stageLabel names the stage in the report and its filename. The execution stage is called "final" to a
// reader, because what they asked for is the final outcome rather than an internal stage name.
func (r *ImportReportStream) stageLabel() string {
	if r.stage == model.ImportStageExecution {
		return ImportReportStageFinal
	}
	return string(model.ImportStagePreflight)
}

// Stream writes the report as one JSON object.
//
// It is assembled key by key rather than marshalled from a filled-in struct, because the results and issues
// are unbounded in principle — a job may carry thousands of each — and holding them all to serialize them is
// exactly what a downloadable report must not do. Each element is marshalled on its own, so only one batch is
// ever resident.
func (r *ImportReportStream) Stream(w io.Writer) error {
	out := bufio.NewWriter(w)
	if err := r.writeHeader(out); err != nil {
		return err
	}
	if err := r.writeResults(out); err != nil {
		return err
	}
	if err := r.writeIssues(out); err != nil {
		return err
	}
	if _, err := out.WriteString("}\n"); err != nil {
		return errors.Wrap(err, "write import report")
	}
	return out.Flush()
}

// writeHeader writes the report's fixed metadata block.
func (r *ImportReportStream) writeHeader(out *bufio.Writer) error {
	job := r.job
	source := model.ImportReportSource{
		OrganizationId: job.BundleSummary.Source.OrganizationId,
		SpaceKey:       job.BundleSummary.Source.SpaceKey,
		SpaceName:      job.BundleSummary.Source.SpaceName,
		ImportSourceId: job.SelectedImportSourceId,
	}
	target := model.ImportReportTarget{
		Kind:    string(job.TargetKind),
		TeamId:  job.TeamId,
		SpaceId: job.TargetSpaceId,
		Existed: job.TargetSpaceExisted,
	}

	if _, err := out.WriteString("{"); err != nil {
		return errors.Wrap(err, "write import report")
	}
	for _, field := range []struct {
		key   string
		value any
	}{
		{"report_version", model.ImportReportVersion},
		{"stage", r.stageLabel()},
		{"job_id", job.Id},
		{"generated_at", r.generatedAt},
		{"source", source},
		{"target", target},
		// The fidelity block is fixed and states the importer's policy, never an assertion about this job's
		// actual outcomes. Those are the result counts below.
		{"fidelity", model.NewImportFidelity()},
		{"counts", r.counts},
	} {
		if err := writeJSONField(out, field.key, field.value); err != nil {
			return err
		}
		if _, err := out.WriteString(","); err != nil {
			return errors.Wrap(err, "write import report")
		}
	}
	return nil
}

// writeResults streams the report's entity outcomes.
func (r *ImportReportStream) writeResults(out *bufio.Writer) error {
	if _, err := out.WriteString(`"results":[`); err != nil {
		return errors.Wrap(err, "write import report")
	}
	written := 0
	after := -1
	for {
		records, err := r.svc.store.GetImportResultsAfter(r.job.Id, r.resultStage, after, importReportBatch)
		if err != nil {
			return errors.Wrap(err, "read import report results")
		}
		if len(records) == 0 {
			break
		}
		for _, record := range records {
			if err = writeJSONElement(out, written, importReportResultOf(record)); err != nil {
				return err
			}
			written++
			after = record.Ordinal
		}
		if len(records) < importReportBatch {
			break
		}
	}
	if _, err := out.WriteString(`],`); err != nil {
		return errors.Wrap(err, "write import report")
	}
	return nil
}

// writeIssues streams the report's findings, one stage after another so the order is deterministic.
func (r *ImportReportStream) writeIssues(out *bufio.Writer) error {
	if _, err := out.WriteString(`"issues":[`); err != nil {
		return errors.Wrap(err, "write import report")
	}
	written := 0
	for _, stage := range r.issueStages {
		after := -1
		for {
			records, err := r.svc.store.GetImportIssuesAfter(r.job.Id, stage, after, importReportBatch)
			if err != nil {
				return errors.Wrap(err, "read import report issues")
			}
			if len(records) == 0 {
				break
			}
			for _, record := range records {
				if err = writeJSONElement(out, written, importIssueViewOf(record)); err != nil {
					return err
				}
				written++
				after = record.Ordinal
			}
			if len(records) < importReportBatch {
				break
			}
		}
	}
	if _, err := out.WriteString(`]`); err != nil {
		return errors.Wrap(err, "write import report")
	}
	return nil
}

// importReportResultOf projects a persisted result row onto the report's result shape.
//
// It carries the plan alongside the outcome so a reader can see both what was expected and what happened,
// which is how a not-attempted page still says what it would have been. Hashes, mapping timestamps, bodies,
// and raw source props are deliberately absent: a report explains outcomes, and the baselines that make
// applying them safe stay server-side.
func importReportResultOf(r *model.ImportResultRecord) model.ImportResult {
	return model.ImportResult{
		Stage: string(r.Stage),
		Entity: model.ImportEntityRef{
			Type:       r.EntityType,
			ExternalId: r.ExternalId,
			LocalId:    r.LocalId,
			Title:      r.Title,
		},
		PlannedAction: string(r.PlannedAction),
		ActualAction:  string(r.ActualAction),
		Outcome:       string(r.Outcome),
		Details:       r.Details,
	}
}

// writeJSONField writes a `"key":<value>` pair.
func writeJSONField(out *bufio.Writer, key string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return errors.Wrapf(err, "marshal import report field %s", key)
	}
	if _, err = out.WriteString(`"` + key + `":`); err != nil {
		return errors.Wrap(err, "write import report")
	}
	if _, err = out.Write(encoded); err != nil {
		return errors.Wrap(err, "write import report")
	}
	return nil
}

// writeJSONElement writes one array element, prefixing a comma for every element after the first.
func writeJSONElement(out *bufio.Writer, index int, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return errors.Wrap(err, "marshal import report element")
	}
	if index > 0 {
		if _, err = out.WriteString(","); err != nil {
			return errors.Wrap(err, "write import report")
		}
	}
	if _, err = out.Write(encoded); err != nil {
		return errors.Wrap(err, "write import report")
	}
	return nil
}

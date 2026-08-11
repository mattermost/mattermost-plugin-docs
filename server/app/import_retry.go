// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"sync"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// ImportErrorRetriesExhausted marks a job the worker stopped retrying after repeated failed passes.
const ImportErrorRetriesExhausted = "retries_exhausted"

// Retry pacing for jobs whose worker pass failed.
//
// A failed pass leaves its job in the state it was selected from, and selection is strictly ordered by state,
// so nothing stops the next pass choosing the same job again — for as long as whatever it is failing on keeps
// failing. That is not a retry loop, it is a queue that has stopped: a fault specific to one target (a Space
// whose authorization cannot be resolved, a channel API that keeps erroring) holds up every unrelated import
// behind it, and none of the states involved are expirable, so nothing else ever clears it.
//
// Two rules follow. A job that fails steps aside long enough for the worker to pick up something else, and a
// job that keeps failing eventually stops being retried and is failed with a report — an import that will
// never finish is more use to its owner as a stated failure than as an unexplained wait.
const (
	// importJobRetryCooldownMillis is how long a failed job is passed over. It is deliberately longer than
	// importWorkerErrorBackoff: the worker already waits that long after a failed pass, so a cooldown merely
	// equal to it would expire exactly as the next selection ran, and the same job would win again.
	importJobRetryCooldownMillis = int64(60 * 1000)

	// importJobRetryLimit is how many consecutive failed passes a job gets before the worker gives up on it.
	// With the cooldown above, reaching it takes about ten minutes of continuous failure.
	importJobRetryLimit = 10
)

// importRetryTracker records consecutive failed passes per job, and until when each should be passed over.
//
// It is per-process and deliberately not durable. A restart clears it, which is the right default twice over:
// the supported topology is a single worker on a single node, so there is no other node's record to reconcile,
// and a restart is itself a plausible fix for whatever the job was failing on — resuming with the attempt
// count already spent would fail a job the restart had just repaired.
type importRetryTracker struct {
	mu   sync.Mutex
	jobs map[string]importRetryEntry
}

// importRetryEntry is one job's failure record. until is a millisecond timestamp.
type importRetryEntry struct {
	attempts int
	until    int64
}

// deferred returns the job ids that should not be selected yet, pruning records whose cooldown has passed.
//
// Pruning here rather than on a timer is what bounds the map: an id is only ever added by a failed pass, and
// every pass drops the ones no longer holding anything back.
func (t *importRetryTracker) deferred(now int64) []string {
	t.mu.Lock()
	defer t.mu.Unlock()

	var ids []string
	for id, entry := range t.jobs {
		if entry.until <= now {
			// The cooldown has expired, but the attempt count has not: that is cleared only by a pass which
			// succeeds, so a job failing ten times in a row is recognized as such however long it takes.
			entry.until = 0
			t.jobs[id] = entry
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

// failed records a failed pass and reports whether the job has run out of attempts.
func (t *importRetryTracker) failed(jobID string, now int64) (attempts int, exhausted bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.jobs == nil {
		t.jobs = map[string]importRetryEntry{}
	}
	entry := t.jobs[jobID]
	entry.attempts++
	entry.until = now + importJobRetryCooldownMillis
	t.jobs[jobID] = entry
	return entry.attempts, entry.attempts >= importJobRetryLimit
}

// forget drops a job's record, so a later failure starts from a clean count. Only *consecutive* failures
// should exhaust the budget: a job that failed, recovered, and fails again next week has not stopped making
// progress.
func (t *importRetryTracker) forget(jobID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.jobs, jobID)
}

// resetAttempts keeps a job's cooldown but clears its count, for the case where giving up is not available.
func (t *importRetryTracker) resetAttempts(jobID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	entry, ok := t.jobs[jobID]
	if !ok {
		return
	}
	entry.attempts = 0
	t.jobs[jobID] = entry
}

// ClearImportRetryCooldowns makes every deferred job eligible again at once, without forgiving any attempt.
//
// The cooldown is wall-clock, which is right for a worker and awkward for anything that has just repaired the
// fault a job was failing on and wants the retry now rather than in a minute — a test, or an operator who has
// fixed the underlying problem. Bringing the retry forward is always safe: the cooldown exists to stop a
// failing job monopolizing the worker, not to protect the job itself.
//
// The attempt counts deliberately survive. They are what bounds the retrying, and a caller able to reset them
// could keep a hopeless job in the queue indefinitely — which is the state this policy exists to end. Only a
// pass that actually succeeds clears a job's record.
func (s *Service) ClearImportRetryCooldowns() {
	s.importRetries.mu.Lock()
	defer s.importRetries.mu.Unlock()

	for id, entry := range s.importRetries.jobs {
		entry.until = 0
		s.importRetries.jobs[id] = entry
	}
}

// giveUpOnImportJob stops retrying a job that has failed importJobRetryLimit passes in a row.
//
// A job still on its way somewhere is failed, so its owner gets a report naming what went wrong rather than an
// import that quietly never finishes. A job already terminalizing has no such option — it has decided its
// outcome and is only trying to write it down, and there is no state to fail it into — so it keeps its place
// in the queue behind its cooldown, and its count restarts so the failure is re-reported periodically instead
// of once.
func (s *Service) giveUpOnImportJob(job *model.ImportJob, attempts int, cause error) error {
	if job.State == model.ImportStateTerminalizing {
		s.log.Error("Import terminalization keeps failing; it stays queued and will be retried on a cooldown",
			"job_id", job.Id, "attempts", attempts, "err", cause)
		s.importRetries.resetAttempts(job.Id)
		return nil
	}

	s.log.Error("Import job failed too many consecutive passes; giving up on it",
		"job_id", job.Id, "state", string(job.State), "attempts", attempts, "err", cause)
	s.importRetries.forget(job.Id)
	return s.failImportJob(job.Id, ImportErrorRetriesExhausted, cause)
}

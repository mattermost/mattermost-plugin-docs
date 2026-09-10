// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"errors"
	"testing"
)

// TestImportRetryTracker_DefersAndBoundsFailures pins the two jobs the tracker does at once: passing a failing
// job over so others get their turn, and noticing when a job has stopped getting anywhere.
func TestImportRetryTracker_DefersAndBoundsFailures(t *testing.T) {
	tracker := &importRetryTracker{}
	const now = int64(1_000_000)

	if deferred := tracker.deferred(now); len(deferred) != 0 {
		t.Fatalf("nothing has failed yet, so nothing should be deferred: %v", deferred)
	}

	attempts, exhausted := tracker.failed("job1", now)
	if attempts != 1 || exhausted {
		t.Fatalf("first failure: attempts = %d, exhausted = %v", attempts, exhausted)
	}

	// Deferral has to outlast the worker's own 30-second error backoff, or the next pass picks the same job
	// again and the queue behind it never moves.
	if deferred := tracker.deferred(now + 30_000); len(deferred) != 1 {
		t.Fatalf("a job that just failed must still be deferred one worker backoff later: %v", deferred)
	}
	if deferred := tracker.deferred(now + importJobRetryCooldownMillis); len(deferred) != 0 {
		t.Fatalf("the cooldown must expire: %v", deferred)
	}

	// The count survives the cooldown expiring: only a pass that succeeds clears it.
	attempts, _ = tracker.failed("job1", now)
	if attempts != 2 {
		t.Fatalf("attempts = %d, want the count to carry across an expired cooldown", attempts)
	}

	tracker.forget("job1")
	attempts, _ = tracker.failed("job1", now)
	if attempts != 1 {
		t.Fatalf("attempts = %d, want a clean count after a successful pass", attempts)
	}
}

// TestImportRetryTracker_ExhaustsAtTheLimit pins where giving up happens.
func TestImportRetryTracker_ExhaustsAtTheLimit(t *testing.T) {
	tracker := &importRetryTracker{}

	for i := 1; i < ImportJobRetryLimit; i++ {
		if _, exhausted := tracker.failed("job1", int64(i)); exhausted {
			t.Fatalf("gave up after %d failures, before the limit of %d", i, ImportJobRetryLimit)
		}
	}
	if _, exhausted := tracker.failed("job1", 0); !exhausted {
		t.Fatalf("never gave up, after %d consecutive failures", ImportJobRetryLimit)
	}
}

// TestImportRetryTracker_ProgressDoesNotSpendAttempts covers the difference between a job that is failing and a
// job that is failing *while getting somewhere*.
//
// A large import rechecks authorization every hundred pages, so a lookup that is merely slow to answer can end a
// pass that wrote a hundred pages. Counting those as failures spends the budget on an import that is steadily
// finishing and then kills it — the opposite of what bounding retries is for.
func TestImportRetryTracker_ProgressDoesNotSpendAttempts(t *testing.T) {
	tracker := &importRetryTracker{}
	const now = int64(1_000_000)

	for range ImportJobRetryLimit * 3 {
		tracker.progressed("job1", now)
	}

	// It still steps aside — fairness to other imports does not depend on why this one stopped.
	if deferred := tracker.deferred(now); len(deferred) != 1 {
		t.Fatalf("a progressing job must still take its cooldown: %v", deferred)
	}

	// But its budget is intact, so the next genuine failure starts from one.
	attempts, exhausted := tracker.failed("job1", now)
	if attempts != 1 || exhausted {
		t.Fatalf("attempts = %d, exhausted = %v; progress must not count against the budget", attempts, exhausted)
	}
}

// TestImportMadeProgress pins the classification the wiring depends on. Only a retryable failure raised after
// durable work qualifies: a plain retryable failure has committed nothing, and a definitive failure is not
// retried at all.
func TestImportMadeProgress(t *testing.T) {
	cause := errors.New("lookup timed out")

	if importMadeProgress(retryableImportError(cause)) {
		t.Errorf("a retryable failure with no work behind it must not count as progress")
	}
	if importMadeProgress(retryableImportErrorAfterProgress(cause, false)) {
		t.Errorf("a pass that applied nothing must not count as progress")
	}
	if !importMadeProgress(retryableImportErrorAfterProgress(cause, true)) {
		t.Errorf("a pass that applied pages before failing must count as progress")
	}
	if importMadeProgress(cause) {
		t.Errorf("an ordinary error must not count as progress")
	}
}

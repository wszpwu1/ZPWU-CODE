package handlers

import (
	"context"
	"testing"
	"time"

	"github.com/wszpwu1/ZPWU-CODE/internal/agent"
)

func TestApprovalStoreBeginBeforeDecide(t *testing.T) {
	store := newApprovalStore()
	ch, err := store.begin("call-1", "alice")
	if err != nil {
		t.Fatalf("begin returned error: %v", err)
	}
	defer store.end("call-1")

	done := make(chan error, 1)
	go func() {
		done <- store.decide("call-1", "alice", agent.ApprovalDecision{Approved: true})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	approved, err := store.wait(ctx, ch)
	if err != nil {
		t.Fatalf("wait returned error: %v", err)
	}
	if !approved {
		t.Fatalf("expected approved decision")
	}
	if err := <-done; err != nil {
		t.Fatalf("decide returned error: %v", err)
	}
}

func TestApprovalStoreRejectsOtherUser(t *testing.T) {
	store := newApprovalStore()
	ch, err := store.begin("call-2", "alice")
	if err != nil {
		t.Fatalf("begin returned error: %v", err)
	}
	defer store.end("call-2")

	if err := store.decide("call-2", "bob", agent.ApprovalDecision{Approved: true}); err == nil {
		t.Fatalf("expected cross-user approval to be rejected")
	}

	select {
	case <-ch:
		t.Fatalf("unexpected decision delivered for rejected approver")
	default:
	}
}

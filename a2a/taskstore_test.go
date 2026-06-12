package a2a

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestMemoryStoreCRUD(t *testing.T) {
	s := newMemoryStore(8)
	ctx := context.Background()
	e := &TaskRecord{Task: Task{ID: "t1", Status: TaskStatus{State: TaskStateSubmitted}}}
	if err := s.Save(ctx, e); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "t1")
	if err != nil || got.Task.ID != "t1" {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	if _, err := s.Get(ctx, "missing"); !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("missing task: want ErrTaskNotFound, got %v", err)
	}
}

// TestMemoryStoreBounded: the store must evict oldest TERMINAL tasks at
// capacity — never evict live (working/input-required) tasks.
func TestMemoryStoreBounded(t *testing.T) {
	s := newMemoryStore(2)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		s.Save(ctx, &TaskRecord{Task: Task{
			ID:     fmt.Sprintf("t%d", i),
			Status: TaskStatus{State: TaskStateCompleted},
		}})
	}
	if _, err := s.Get(ctx, "t0"); !errors.Is(err, ErrTaskNotFound) {
		t.Error("t0 should have been evicted")
	}
	if _, err := s.Get(ctx, "t2"); err != nil {
		t.Error("t2 must survive")
	}
}

// TestMemoryStoreNeverEvictsLive: live tasks survive even over capacity.
func TestMemoryStoreNeverEvictsLive(t *testing.T) {
	s := newMemoryStore(2)
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		s.Save(ctx, &TaskRecord{Task: Task{
			ID:     fmt.Sprintf("live%d", i),
			Status: TaskStatus{State: TaskStateWorking},
		}})
	}
	for i := 0; i < 4; i++ {
		if _, err := s.Get(ctx, fmt.Sprintf("live%d", i)); err != nil {
			t.Errorf("live%d must never be evicted", i)
		}
	}
}

func TestMemoryStoreList(t *testing.T) {
	s := newMemoryStore(8)
	ctx := context.Background()
	s.Save(ctx, &TaskRecord{Task: Task{ID: "a", ContextID: "ctx1", Status: TaskStatus{State: TaskStateCompleted}}})
	s.Save(ctx, &TaskRecord{Task: Task{ID: "b", ContextID: "ctx2", Status: TaskStatus{State: TaskStateCompleted}}})
	s.Save(ctx, &TaskRecord{Task: Task{ID: "c", ContextID: "ctx1", Status: TaskStatus{State: TaskStateWorking}}})
	got, err := s.List(ctx, "ctx1")
	if err != nil || len(got) != 2 {
		t.Fatalf("List = %d entries, %v", len(got), err)
	}
	if got[0].Task.ID != "c" { // newest first
		t.Errorf("List order: want newest first, got %s", got[0].Task.ID)
	}
}

func TestMemoryStoreConcurrent(t *testing.T) {
	s := newMemoryStore(1024)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := fmt.Sprintf("t%d", n)
			s.Save(ctx, &TaskRecord{Task: Task{ID: id, Status: TaskStatus{State: TaskStateWorking}}})
			s.Get(ctx, id)
		}(i)
	}
	wg.Wait()
}

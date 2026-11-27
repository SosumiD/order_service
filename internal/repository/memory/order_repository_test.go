package memory_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"example.com/order-service/internal/order"
	"example.com/order-service/internal/repository/memory"
)

func TestRepository(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewOrderRepository()
	empty, err := repo.List(ctx)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("wrong empty list: %+v, %v", empty, err)
	}
	first, err := repo.Create(ctx, order.Order{Items: []order.Item{{Name: "A"}}, History: []order.StatusChange{{Status: order.StatusNew}}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.Create(ctx, order.Order{Items: []order.Item{{Name: "B"}}})
	if err != nil || second.ID != first.ID+1 {
		t.Fatalf("wrong ids: %+v %+v %v", first, second, err)
	}
	first.Items[0].Name = "changed"
	first.History[0].Status = order.StatusCompleted
	stored, err := repo.GetByID(ctx, first.ID)
	if err != nil || stored.Items[0].Name != "A" || stored.History[0].Status != order.StatusNew {
		t.Fatalf("internal state was modified: %+v, %v", stored, err)
	}
	values, err := repo.List(ctx)
	if err != nil || len(values) != 2 || values[0].ID != first.ID || values[1].ID != second.ID {
		t.Fatalf("wrong list: %+v, %v", values, err)
	}
	values[0].Items[0].Name = "broken"
	stored, _ = repo.GetByID(ctx, first.ID)
	if stored.Items[0].Name != "A" {
		t.Fatal("list exposed mutable internal state")
	}
	stored.Status = order.StatusProcessing
	stored.Items[0].Name = "Updated"
	updated, err := repo.Update(ctx, stored)
	if err != nil || updated.Status != order.StatusProcessing {
		t.Fatalf("failed update: %+v, %v", updated, err)
	}
	updated.Items[0].Name = "external"
	stored, _ = repo.GetByID(ctx, first.ID)
	if stored.Items[0].Name != "Updated" {
		t.Fatal("update exposed mutable internal state")
	}
	if _, err := repo.GetByID(ctx, 999); !errors.Is(err, order.ErrOrderNotFound) {
		t.Fatalf("get missing: %v", err)
	}
	if _, err := repo.Update(ctx, order.Order{ID: 999}); !errors.Is(err, order.ErrOrderNotFound) {
		t.Fatalf("update missing: %v", err)
	}
}

func TestConcurrentCreation(t *testing.T) {
	repo := memory.NewOrderRepository()
	const count = 100
	ids := make(chan int64, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, err := repo.Create(context.Background(), order.Order{})
			if err != nil {
				t.Error(err)
				return
			}
			ids <- value.ID
		}()
	}
	wg.Wait()
	close(ids)
	seen := make(map[int64]bool)
	for id := range ids {
		if seen[id] {
			t.Fatalf("duplicate id %d", id)
		}
		seen[id] = true
	}
	if len(seen) != count {
		t.Fatalf("created %d orders, want %d", len(seen), count)
	}
}

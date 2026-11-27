package service_test

import (
	"context"
	"errors"
	"math"
	"sort"
	"testing"
	"time"

	"example.com/order-service/internal/order"
	"example.com/order-service/internal/service"
)

type fakeRepository struct {
	orders    map[int64]order.Order
	nextID    int64
	createErr error
	getErr    error
	listErr   error
	updateErr error
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{orders: make(map[int64]order.Order), nextID: 1}
}

func (r *fakeRepository) Create(_ context.Context, value order.Order) (order.Order, error) {
	if r.createErr != nil {
		return order.Order{}, r.createErr
	}
	value.ID = r.nextID
	r.nextID++
	r.orders[value.ID] = value
	return value, nil
}

func (r *fakeRepository) GetByID(_ context.Context, id int64) (order.Order, error) {
	if r.getErr != nil {
		return order.Order{}, r.getErr
	}
	value, ok := r.orders[id]
	if !ok {
		return order.Order{}, order.ErrOrderNotFound
	}
	return value, nil
}

func (r *fakeRepository) List(_ context.Context) ([]order.Order, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	ids := make([]int64, 0, len(r.orders))
	for id := range r.orders {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	result := make([]order.Order, 0, len(ids))
	for _, id := range ids {
		result = append(result, r.orders[id])
	}
	return result, nil
}

func (r *fakeRepository) Update(_ context.Context, value order.Order) (order.Order, error) {
	if r.updateErr != nil {
		return order.Order{}, r.updateErr
	}
	if _, ok := r.orders[value.ID]; !ok {
		return order.Order{}, order.ErrOrderNotFound
	}
	r.orders[value.ID] = value
	return value, nil
}

func TestCreate(t *testing.T) {
	tests := []struct {
		name  string
		items []order.Item
		total int64
		valid bool
	}{
		{"single item", []order.Item{{Name: "Keyboard", Quantity: 2, Price: 500}}, 1000, true},
		{"multiple items", []order.Item{{Name: "A", Quantity: 2, Price: 30}, {Name: "B", Quantity: 3, Price: 40}}, 180, true},
		{"no items", nil, 0, false},
		{"empty name", []order.Item{{Name: "", Quantity: 1, Price: 1}}, 0, false},
		{"whitespace name", []order.Item{{Name: "  ", Quantity: 1, Price: 1}}, 0, false},
		{"zero quantity", []order.Item{{Name: "A", Quantity: 0, Price: 1}}, 0, false},
		{"negative quantity", []order.Item{{Name: "A", Quantity: -1, Price: 1}}, 0, false},
		{"zero price", []order.Item{{Name: "A", Quantity: 1, Price: 0}}, 0, false},
		{"negative price", []order.Item{{Name: "A", Quantity: 1, Price: -1}}, 0, false},
		{"invalid second item", []order.Item{{Name: "A", Quantity: 1, Price: 10}, {Name: "B", Quantity: -1, Price: 10}}, 0, false},
		{"multiplication overflow", []order.Item{{Name: "A", Quantity: 2, Price: math.MaxInt64}}, 0, false},
		{"sum overflow", []order.Item{{Name: "A", Quantity: 1, Price: math.MaxInt64}, {Name: "B", Quantity: 1, Price: 1}}, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository := newFakeRepository()
			value, err := service.NewOrderService(repository).Create(context.Background(), order.CreateOrderInput{Items: tt.items})
			if !tt.valid {
				if !errors.Is(err, order.ErrInvalidOrder) {
					t.Fatalf("expected invalid order, got %v", err)
				}
				if len(repository.orders) != 0 {
					t.Fatal("invalid order was saved")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if value.ID != 1 || value.TotalPrice != tt.total || value.Status != order.StatusNew {
				t.Fatalf("wrong order: %+v", value)
			}
			if value.CreatedAt.IsZero() || len(value.History) != 1 || value.History[0].Status != order.StatusNew {
				t.Fatalf("wrong initial history: %+v", value)
			}
			if !value.History[0].ChangedAt.Equal(value.CreatedAt) {
				t.Fatal("creation timestamp differs from initial status timestamp")
			}
		})
	}
}

func TestRead(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepository()
	svc := service.NewOrderService(repo)
	first, err := svc.Create(ctx, order.CreateOrderInput{Items: []order.Item{{Name: "A", Quantity: 1, Price: 5}}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Create(ctx, order.CreateOrderInput{Items: []order.Item{{Name: "B", Quantity: 2, Price: 6}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		id   int64
		want int64
		fail bool
	}{
		{first.ID, 5, false},
		{second.ID, 12, false},
		{999, 0, true},
	} {
		value, err := svc.GetByID(ctx, tt.id)
		if tt.fail && !errors.Is(err, order.ErrOrderNotFound) {
			t.Fatalf("expected not found, got %v", err)
		}
		if !tt.fail && (err != nil || value.TotalPrice != tt.want) {
			t.Fatalf("wrong order for id=%d: %+v, %v", tt.id, value, err)
		}
	}
	list, err := svc.List(ctx)
	if err != nil || len(list) != 2 || list[0].ID != first.ID || list[1].ID != second.ID {
		t.Fatalf("wrong list: %+v, %v", list, err)
	}
	history, err := svc.History(ctx, first.ID)
	if err != nil || len(history) != 1 || history[0].Status != order.StatusNew {
		t.Fatalf("wrong history: %+v, %v", history, err)
	}
	history[0].Status = order.StatusCompleted
	original, _ := svc.History(ctx, first.ID)
	if original[0].Status != order.StatusNew {
		t.Fatal("history was not copied")
	}
	if _, err := svc.History(ctx, 999); !errors.Is(err, order.ErrOrderNotFound) {
		t.Fatalf("expected not found history, got %v", err)
	}
}

func TestStatusTransitions(t *testing.T) {
	tests := []struct {
		name string
		from order.Status
		to   order.Status
		want error
	}{
		{"new to processing", order.StatusNew, order.StatusProcessing, nil},
		{"new to cancelled", order.StatusNew, order.StatusCancelled, nil},
		{"processing to completed", order.StatusProcessing, order.StatusCompleted, nil},
		{"processing to cancelled", order.StatusProcessing, order.StatusCancelled, nil},
		{"new to completed", order.StatusNew, order.StatusCompleted, order.ErrInvalidStatusTransition},
		{"new to new", order.StatusNew, order.StatusNew, order.ErrInvalidStatusTransition},
		{"processing to new", order.StatusProcessing, order.StatusNew, order.ErrInvalidStatusTransition},
		{"processing to processing", order.StatusProcessing, order.StatusProcessing, order.ErrInvalidStatusTransition},
		{"completed to cancelled", order.StatusCompleted, order.StatusCancelled, order.ErrInvalidStatusTransition},
		{"completed to completed", order.StatusCompleted, order.StatusCompleted, order.ErrInvalidStatusTransition},
		{"cancelled to processing", order.StatusCancelled, order.StatusProcessing, order.ErrInvalidStatusTransition},
		{"cancelled to cancelled", order.StatusCancelled, order.StatusCancelled, order.ErrInvalidStatusTransition},
		{"unknown status", order.StatusNew, "wrong", order.ErrInvalidStatus},
		{"empty status", order.StatusNew, "", order.ErrInvalidStatus},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository := newFakeRepository()
			before := time.Now().Add(-time.Minute)
			repository.orders[1] = order.Order{ID: 1, Status: tt.from, History: []order.StatusChange{{Status: tt.from, ChangedAt: before}}}
			value, err := service.NewOrderService(repository).UpdateStatus(context.Background(), 1, tt.to)
			if !errors.Is(err, tt.want) {
				t.Fatalf("expected %v, got %v", tt.want, err)
			}
			stored := repository.orders[1]
			if tt.want != nil {
				if stored.Status != tt.from || len(stored.History) != 1 {
					t.Fatalf("invalid transition changed order: %+v", stored)
				}
				return
			}
			if value.Status != tt.to || stored.Status != tt.to || len(stored.History) != 2 {
				t.Fatalf("status was not saved: %+v", stored)
			}
			if stored.History[1].Status != tt.to || stored.History[1].ChangedAt.Before(stored.History[0].ChangedAt) {
				t.Fatalf("wrong history: %+v", stored.History)
			}
		})
	}
}

func TestFullLifecycle(t *testing.T) {
	ctx := context.Background()
	svc := service.NewOrderService(newFakeRepository())
	value, err := svc.Create(ctx, order.CreateOrderInput{Items: []order.Item{{Name: "Mouse", Quantity: 2, Price: 100}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []order.Status{order.StatusProcessing, order.StatusCompleted} {
		if _, err := svc.UpdateStatus(ctx, value.ID, status); err != nil {
			t.Fatal(err)
		}
	}
	history, err := svc.History(ctx, value.ID)
	if err != nil || len(history) != 3 {
		t.Fatalf("wrong history: %+v, %v", history, err)
	}
	for i, status := range []order.Status{order.StatusNew, order.StatusProcessing, order.StatusCompleted} {
		if history[i].Status != status || (i > 0 && history[i].ChangedAt.Before(history[i-1].ChangedAt)) {
			t.Fatalf("wrong chronological order: %+v", history)
		}
	}
	if _, err := svc.UpdateStatus(ctx, value.ID, order.StatusCancelled); !errors.Is(err, order.ErrInvalidStatusTransition) {
		t.Fatalf("expected terminal transition error, got %v", err)
	}
}

func TestRepositoryErrors(t *testing.T) {
	ctx := context.Background()
	failure := errors.New("storage unavailable")
	repo := newFakeRepository()
	svc := service.NewOrderService(repo)
	repo.createErr = failure
	if _, err := svc.Create(ctx, order.CreateOrderInput{Items: []order.Item{{Name: "A", Quantity: 1, Price: 1}}}); !errors.Is(err, failure) {
		t.Fatalf("create error: %v", err)
	}
	repo.createErr = nil
	repo.getErr = failure
	if _, err := svc.GetByID(ctx, 1); !errors.Is(err, failure) {
		t.Fatalf("get error: %v", err)
	}
	if _, err := svc.History(ctx, 1); !errors.Is(err, failure) {
		t.Fatalf("history error: %v", err)
	}
	if _, err := svc.UpdateStatus(ctx, 1, order.StatusProcessing); !errors.Is(err, failure) {
		t.Fatalf("status get error: %v", err)
	}
	repo.getErr = nil
	repo.orders[1] = order.Order{ID: 1, Status: order.StatusNew}
	repo.updateErr = failure
	if _, err := svc.UpdateStatus(ctx, 1, order.StatusProcessing); !errors.Is(err, failure) {
		t.Fatalf("update error: %v", err)
	}
	repo.listErr = failure
	if _, err := svc.List(ctx); !errors.Is(err, failure) {
		t.Fatalf("list error: %v", err)
	}
}

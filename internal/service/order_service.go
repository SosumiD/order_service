package service

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"example.com/order-service/internal/order"
)

type OrderService struct {
	repository order.Repository
	mu         sync.Mutex
}

func NewOrderService(repository order.Repository) *OrderService {
	return &OrderService{repository: repository}
}

func (s *OrderService) Create(ctx context.Context, input order.CreateOrderInput) (order.Order, error) {
	if len(input.Items) == 0 {
		return order.Order{}, fmt.Errorf("%w: items must not be empty", order.ErrInvalidOrder)
	}

	var total int64
	for i, item := range input.Items {
		if strings.TrimSpace(item.Name) == "" || item.Quantity <= 0 || item.Price <= 0 {
			return order.Order{}, fmt.Errorf("%w: item %d has invalid fields", order.ErrInvalidOrder, i+1)
		}
		if int64(item.Quantity) > math.MaxInt64/item.Price || total > math.MaxInt64-item.Price*int64(item.Quantity) {
			return order.Order{}, fmt.Errorf("%w: total price overflow", order.ErrInvalidOrder)
		}
		total += item.Price * int64(item.Quantity)
	}

	now := time.Now().UTC()
	return s.repository.Create(ctx, order.Order{
		Items:      append([]order.Item(nil), input.Items...),
		TotalPrice: total,
		Status:     order.StatusNew,
		CreatedAt:  now,
		History:    []order.StatusChange{{Status: order.StatusNew, ChangedAt: now}},
	})
}

func (s *OrderService) GetByID(ctx context.Context, id int64) (order.Order, error) {
	return s.repository.GetByID(ctx, id)
}

func (s *OrderService) List(ctx context.Context) ([]order.Order, error) {
	return s.repository.List(ctx)
}

func (s *OrderService) UpdateStatus(ctx context.Context, id int64, status order.Status) (order.Order, error) {
	if status != order.StatusNew && status != order.StatusProcessing && status != order.StatusCompleted && status != order.StatusCancelled {
		return order.Order{}, order.ErrInvalidStatus
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	value, err := s.repository.GetByID(ctx, id)
	if err != nil {
		return order.Order{}, err
	}

	allowed := (value.Status == order.StatusNew && (status == order.StatusProcessing || status == order.StatusCancelled)) ||
		(value.Status == order.StatusProcessing && (status == order.StatusCompleted || status == order.StatusCancelled))
	if !allowed {
		return order.Order{}, order.ErrInvalidStatusTransition
	}

	value.Status = status
	value.History = append(value.History, order.StatusChange{Status: status, ChangedAt: time.Now().UTC()})
	return s.repository.Update(ctx, value)
}

func (s *OrderService) History(ctx context.Context, id int64) ([]order.StatusChange, error) {
	value, err := s.repository.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return append([]order.StatusChange(nil), value.History...), nil
}

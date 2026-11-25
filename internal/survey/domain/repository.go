package domain

import "context"

// Repository is the write-side port for the Survey aggregate. Implementations
// must persist the aggregate's pending events atomically with its state.
type Repository interface {
	Add(ctx context.Context, s *Survey) error
	// Update loads the survey, applies mutate and saves the result in one
	// transaction. If mutate returns an error nothing is saved.
	Update(ctx context.Context, id string, mutate func(s *Survey) error) error
}

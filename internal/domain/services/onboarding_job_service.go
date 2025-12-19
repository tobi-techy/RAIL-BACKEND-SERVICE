package services

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

// OnboardingJobService handles onboarding job business logic
type OnboardingJobService struct {
	jobRepo OnboardingJobRepository
	logger  *zap.Logger
}

// OnboardingJobRepository interface for dependency injection
type OnboardingJobRepository interface {
	Create(ctx context.Context, job *entities.OnboardingJob) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.OnboardingJob, error)
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.OnboardingJob, error)
	GetPendingJobs(ctx context.Context, limit int) ([]*entities.OnboardingJob, error)
	Update(ctx context.Context, job *entities.OnboardingJob) error
	Delete(ctx context.Context, id uuid.UUID) error
	GetJobStats(ctx context.Context) (map[string]int, error)
}

// NewOnboardingJobService creates a new onboarding job service
func NewOnboardingJobService(jobRepo OnboardingJobRepository, logger *zap.Logger) *OnboardingJobService {
	return &OnboardingJobService{
		jobRepo: jobRepo,
		logger:  logger,
	}
}

// CreateOnboardingJob creates a new onboarding job for a user
func (s *OnboardingJobService) CreateOnboardingJob(ctx context.Context, userID uuid.UUID, userEmail, userPhone string) (*entities.OnboardingJob, error) {
	s.logger.Info("Creating onboarding job",
		zap.String("user_id", userID.String()),
		zap.String("email", s.maskEmail(userEmail)),
		zap.String("phone", s.maskPhone(userPhone)))

	// Check if user already has an active onboarding job
	existingJob, err := s.jobRepo.GetByUserID(ctx, userID)
	if err == nil && existingJob != nil {
		// Check if existing job is still active
		if existingJob.Status == entities.OnboardingJobStatusQueued ||
			existingJob.Status == entities.OnboardingJobStatusInProgress ||
			existingJob.Status == entities.OnboardingJobStatusRetry {

			s.logger.Info("User already has active onboarding job",
				zap.String("user_id", userID.String()),
				zap.String("job_id", existingJob.ID.String()),
				zap.String("status", string(existingJob.Status)))

			return existingJob, nil
		}
	}

	// Create new onboarding job
	job := &entities.OnboardingJob{
		ID:           uuid.New(),
		UserID:       userID,
		Status:       entities.OnboardingJobStatusQueued,
		JobType:      entities.OnboardingJobTypeWalletOnly,
		AttemptCount: 0,
		MaxAttempts:  5,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	// Set up job payload with user information
	payload := entities.OnboardingJobPayload{
		UserEmail: userEmail,
		UserPhone: userPhone,
		WalletChains: []string{
			string(entities.WalletChainSOLDevnet),
		},
		Metadata: map[string]interface{}{
			"created_by": "signup_flow",
			"priority":   "normal",
		},
	}

	job.Payload = map[string]interface{}{
		"user_email":    payload.UserEmail,
		"user_phone":    payload.UserPhone,
		"wallet_chains": payload.WalletChains,
		"metadata":      payload.Metadata,
	}

	// Validate job before creating
	if err := job.Validate(); err != nil {
		s.logger.Error("Invalid onboarding job",
			zap.String("user_id", userID.String()),
			zap.Error(err))
		return nil, fmt.Errorf("invalid onboarding job: %w", err)
	}

	// Create job in database
	if err := s.jobRepo.Create(ctx, job); err != nil {
		s.logger.Error("Failed to create onboarding job",
			zap.String("user_id", userID.String()),
			zap.Error(err))
		return nil, fmt.Errorf("failed to create onboarding job: %w", err)
	}

	s.logger.Info("Created onboarding job successfully",
		zap.String("job_id", job.ID.String()),
		zap.String("user_id", userID.String()))

	return job, nil
}

// GetOnboardingJob retrieves an onboarding job by ID
func (s *OnboardingJobService) GetOnboardingJob(ctx context.Context, jobID uuid.UUID) (*entities.OnboardingJob, error) {
	job, err := s.jobRepo.GetByID(ctx, jobID)
	if err != nil {
		s.logger.Error("Failed to get onboarding job",
			zap.String("job_id", jobID.String()),
			zap.Error(err))
		return nil, fmt.Errorf("failed to get onboarding job: %w", err)
	}

	return job, nil
}

// GetUserOnboardingJob retrieves the latest onboarding job for a user
func (s *OnboardingJobService) GetUserOnboardingJob(ctx context.Context, userID uuid.UUID) (*entities.OnboardingJob, error) {
	job, err := s.jobRepo.GetByUserID(ctx, userID)
	if err != nil {
		s.logger.Error("Failed to get user onboarding job",
			zap.String("user_id", userID.String()),
			zap.Error(err))
		return nil, fmt.Errorf("failed to get user onboarding job: %w", err)
	}

	return job, nil
}

// GetPendingJobs retrieves jobs that are ready for processing
func (s *OnboardingJobService) GetPendingJobs(ctx context.Context, limit int) ([]*entities.OnboardingJob, error) {
	jobs, err := s.jobRepo.GetPendingJobs(ctx, limit)
	if err != nil {
		s.logger.Error("Failed to get pending onboarding jobs", zap.Error(err))
		return nil, fmt.Errorf("failed to get pending jobs: %w", err)
	}

	s.logger.Debug("Retrieved pending onboarding jobs",
		zap.Int("count", len(jobs)))

	return jobs, nil
}

// UpdateOnboardingJob updates an onboarding job
func (s *OnboardingJobService) UpdateOnboardingJob(ctx context.Context, job *entities.OnboardingJob) error {
	job.UpdatedAt = time.Now()

	if err := s.jobRepo.Update(ctx, job); err != nil {
		s.logger.Error("Failed to update onboarding job",
			zap.String("job_id", job.ID.String()),
			zap.Error(err))
		return fmt.Errorf("failed to update onboarding job: %w", err)
	}

	s.logger.Debug("Updated onboarding job",
		zap.String("job_id", job.ID.String()),
		zap.String("status", string(job.Status)))

	return nil
}

// MarkJobStarted marks a job as started
func (s *OnboardingJobService) MarkJobStarted(ctx context.Context, job *entities.OnboardingJob) error {
	job.MarkStarted()
	return s.UpdateOnboardingJob(ctx, job)
}

// MarkJobCompleted marks a job as completed
func (s *OnboardingJobService) MarkJobCompleted(ctx context.Context, job *entities.OnboardingJob) error {
	job.MarkCompleted()
	return s.UpdateOnboardingJob(ctx, job)
}

// MarkJobFailed marks a job as failed with retry logic
func (s *OnboardingJobService) MarkJobFailed(ctx context.Context, job *entities.OnboardingJob, errorMsg string, retryDelay time.Duration) error {
	job.MarkFailed(errorMsg, retryDelay)
	return s.UpdateOnboardingJob(ctx, job)
}

// GetJobStats returns statistics about onboarding jobs
func (s *OnboardingJobService) GetJobStats(ctx context.Context) (map[string]int, error) {
	stats, err := s.jobRepo.GetJobStats(ctx)
	if err != nil {
		s.logger.Error("Failed to get onboarding job stats", zap.Error(err))
		return nil, fmt.Errorf("failed to get job stats: %w", err)
	}

	s.logger.Debug("Retrieved onboarding job stats", zap.Any("stats", stats))
	return stats, nil
}

// maskEmail masks email for logging
func (s *OnboardingJobService) maskEmail(email string) string {
	if len(email) < 5 {
		return "***"
	}

	atIndex := -1
	for i, char := range email {
		if char == '@' {
			atIndex = i
			break
		}
	}

	if atIndex == -1 {
		return email[:2] + "***"
	}

	if atIndex < 3 {
		return email[:atIndex] + "***@" + email[atIndex+1:]
	}

	return email[:2] + "***@" + email[atIndex+1:]
}

// maskPhone masks phone number for logging
func (s *OnboardingJobService) maskPhone(phone string) string {
	if len(phone) < 7 {
		return "****"
	}
	return phone[:3] + "****" + phone[len(phone)-3:]
}

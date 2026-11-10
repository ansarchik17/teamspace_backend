package service

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/keycloak"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

type identityProviderAdmin interface {
	CreateIdentityProvider(context.Context, string, keycloak.IdentityProviderRepresentation) error
	UpdateIdentityProvider(context.Context, string, string, keycloak.IdentityProviderRepresentation) error
	GetIdentityProvider(context.Context, string, string) (*keycloak.IdentityProviderRepresentation, error)
	DeleteIdentityProvider(context.Context, string, string) error
	CreateIdentityProviderMapper(context.Context, string, string, keycloak.IdentityProviderMapperRepresentation) error
	GetIdentityProviderMappers(context.Context, string, string) ([]keycloak.IdentityProviderMapperRepresentation, error)
}

func (s *OrganizationIdentityProviderService) ForCurrentCompany(ctx context.Context, actor int64) (*OrganizationIdentityProviderService, uuid.UUID, error) {
	repo, org, err := s.repo.ForCurrentCompany(ctx, actor)
	if err != nil {
		return nil, uuid.Nil, err
	}
	clone := *s
	clone.repo = repo
	return &clone, org, nil
}

type providerCompensation func(context.Context) error

func (s *OrganizationIdentityProviderService) mutate(ctx context.Context, actor int64, org uuid.UUID, operation func(*OrganizationIdentityProviderService, *providerCompensation) (*domain.OrganizationIdentityProvider, error)) (*domain.OrganizationIdentityProvider, error) {
	var out *domain.OrganizationIdentityProvider
	var undo providerCompensation
	err := s.repo.WithAdministrationTransaction(ctx, actor, org, func(repo *repository.OrganizationIdentityProviderRepository) error {
		clone := *s
		clone.repo = repo
		var err error
		out, err = operation(&clone, &undo)
		return err
	}, func() {
		if undo == nil {
			return
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if rollbackErr := undo(cleanup); rollbackErr != nil {
			s.logger.Error("identity provider compensation failed; platform reconciliation required", zap.String("organization_id", org.String()))
		}
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *OrganizationIdentityProviderService) Create(ctx context.Context, actor int64, org uuid.UUID, req domain.CreateOrganizationIdentityProviderRequest) (*domain.OrganizationIdentityProvider, error) {
	return s.mutate(ctx, actor, org, func(inner *OrganizationIdentityProviderService, undo *providerCompensation) (*domain.OrganizationIdentityProvider, error) {
		return inner.create(ctx, actor, org, req, undo)
	})
}
func (s *OrganizationIdentityProviderService) Update(ctx context.Context, actor int64, org, provider uuid.UUID, req domain.UpdateOrganizationIdentityProviderRequest) (*domain.OrganizationIdentityProvider, error) {
	return s.mutate(ctx, actor, org, func(inner *OrganizationIdentityProviderService, undo *providerCompensation) (*domain.OrganizationIdentityProvider, error) {
		return inner.update(ctx, actor, org, provider, req, undo)
	})
}
func (s *OrganizationIdentityProviderService) Delete(ctx context.Context, actor int64, org, provider uuid.UUID) error {
	_, err := s.mutate(ctx, actor, org, func(inner *OrganizationIdentityProviderService, undo *providerCompensation) (*domain.OrganizationIdentityProvider, error) {
		return nil, inner.delete(ctx, actor, org, provider, undo)
	})
	return err
}

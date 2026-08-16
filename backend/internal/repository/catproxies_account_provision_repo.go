package repository

import (
	"context"
	"fmt"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type managedAccountProvisionRepository struct {
	client   *dbent.Client
	accounts service.AccountRepository
	runtime  service.ManagedProxyRuntimeRepository
}

func NewManagedAccountProvisionRepository(
	client *dbent.Client,
	accounts service.AccountRepository,
	runtime service.ManagedProxyRuntimeRepository,
) service.ManagedAccountProvisionRepository {
	return &managedAccountProvisionRepository{client: client, accounts: accounts, runtime: runtime}
}

func (r *managedAccountProvisionRepository) Create(
	ctx context.Context,
	account *service.Account,
	groups []service.AccountGroup,
	candidate service.ManagedProxyCandidate,
) error {
	if r == nil || r.client == nil || account == nil {
		return service.ErrAccountNilInput
	}
	creator, ok := r.accounts.(service.AccountDuplicateRepository)
	if !ok || r.runtime == nil {
		return fmt.Errorf("managed account provisioning repository is not configured")
	}

	tx, err := r.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin managed account provision transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)

	if err := creator.CreateWithAccountGroups(txCtx, account, groups); err != nil {
		return fmt.Errorf("create managed account: %w", err)
	}
	for i := range groups {
		groups[i].AccountID = account.ID
	}
	if _, err := r.runtime.Activate(txCtx, account.ID, candidate); err != nil {
		return fmt.Errorf("activate managed proxy: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit managed account provision transaction: %w", err)
	}
	return nil
}

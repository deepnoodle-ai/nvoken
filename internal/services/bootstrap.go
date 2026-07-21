package services

import (
	"context"
	"fmt"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

type bootstrapStore interface {
	ports.AccountRepository
	ports.TenantPartitionRepository
}

// BootstrapInstallation resolves the only Account owned by the static
// self-hosted authenticator. A transaction-scoped database lock makes startup
// safe when several replicas begin together.
func BootstrapInstallation(
	ctx context.Context,
	store bootstrapStore,
	txm ports.TransactionManager,
	clock ports.Clock,
	ids ports.IDGenerator,
) (domain.Account, error) {
	if store == nil || txm == nil || clock == nil || ids == nil {
		return domain.Account{}, fmt.Errorf("installation bootstrap is not configured")
	}
	var account domain.Account
	err := txm.WithTransaction(ctx, func(txCtx context.Context) error {
		if err := store.LockInstallationBootstrap(txCtx); err != nil {
			return fmt.Errorf("lock installation bootstrap: %w", err)
		}
		accounts, err := store.ListAccounts(txCtx)
		if err != nil {
			return fmt.Errorf("list installation Accounts: %w", err)
		}
		switch len(accounts) {
		case 0:
			now := clock.Now().UTC()
			accountID, err := ids.NewID(domain.PrefixAccount)
			if err != nil {
				return err
			}
			partitionID, err := ids.NewID(domain.PrefixTenantPartition)
			if err != nil {
				return err
			}
			account = domain.Account{ID: accountID, CreatedAt: now}
			if err := store.CreateAccount(txCtx, account); err != nil {
				return fmt.Errorf("create installation Account: %w", err)
			}
			if err := store.CreateTenantPartition(txCtx, domain.TenantPartition{
				ID: partitionID, AccountID: account.ID, CreatedAt: now,
			}); err != nil {
				return fmt.Errorf("create installation default tenant partition: %w", err)
			}
		case 1:
			account = accounts[0]
			if _, err := store.GetDefaultTenantPartition(txCtx, account.ID); err != nil {
				return fmt.Errorf("resolve installation default tenant partition: %w", err)
			}
		default:
			return fmt.Errorf("static installation requires exactly one Account; found at least %d", len(accounts))
		}
		return nil
	})
	if err != nil {
		return domain.Account{}, err
	}
	return account, nil
}

package deployer

import (
	"context"
	"fmt"

	"cascata-backend/internal/domain/branching/diff"
)

// DeployMergeResult contém o resultado de uma operação de deploy merge
type DeployMergeResult struct {
	Success      bool
	Message      string
	DiffResult   *diff.DiffResult
	DryRunResult *DryRunResult
	Error        string
	SnapshotName string // Novo campo para rastrear o banco físico de rollback
}

// DeployMerge executa um deploy completo usando o diff engine
// Este é o ponto de entrada principal para operações de deploy de branch
func (d *Deployer) DeployMerge(
	ctx context.Context,
	projectSlug string,
	sourceBranch string,
	targetBranch string,
	opts DeployOptions,
) (*DeployMergeResult, error) {
	d.logger.Info("Starting deploy merge",
		"project", projectSlug,
		"source", sourceBranch,
		"target", targetBranch,
	)

	// 1. Configura o contexto do diff
	diffCtx := diff.DiffContext{
		PoolProvider: d.poolProvider,
		ProjectSlug:  projectSlug,
		SourceBranch: sourceBranch,
		TargetBranch: targetBranch,
		Mode:         diff.ModeBranchToMain,
	}

	// 2. Cria e executa o diff engine
	engine := diff.NewDiffEngine(diffCtx)
	diffResult, err := engine.Run(ctx)
	if err != nil {
		return &DeployMergeResult{
			Success: false,
			Error:   fmt.Errorf("diff generation failed: %w", err).Error(),
		}, err
	}

	if len(diffResult.SQL) == 0 {
		return &DeployMergeResult{
			Success: true,
			Message: "No changes to deploy",
			DiffResult: diffResult,
		}, nil
	}

	d.logger.Info("Diff generated",
		"statements", len(diffResult.SQL),
		"phases", len(diffResult.Summaries),
	)

	// 3. Executa dry run se solicitado
	if opts.DryRun {
		dryRunResult, err := d.RunDryRun(ctx, projectSlug, targetBranch, diffResult.SQL)
		if err != nil {
			return &DeployMergeResult{
				Success: false,
				Error:   fmt.Errorf("dry run failed: %w", err).Error(),
				DiffResult: diffResult,
			}, err
		}

		if !dryRunResult.Success {
			return &DeployMergeResult{
				Success: false,
				Error:   dryRunResult.Error,
				DiffResult: diffResult,
				DryRunResult: dryRunResult,
			}, fmt.Errorf("dry run validation failed: %s", dryRunResult.Error)
		}

		d.logger.Info("Dry run passed",
			"validated", dryRunResult.SQLCount,
		)

		return &DeployMergeResult{
			Success: true,
			Message: "Dry run completed successfully",
			DiffResult: diffResult,
			DryRunResult: dryRunResult,
		}, nil
	}

	// 4. Executa deploy com snapshot de segurança se habilitado
	var snapshotDBName string
	if opts.SafetySnapshot {
		snapshotDBName, err = d.DeployWithSafety(ctx, projectSlug, targetBranch, diffResult.SQL, opts)
	} else {
		err = d.ExecuteDeploy(ctx, projectSlug, targetBranch, diffResult.SQL, opts)
	}

	if err != nil {
		return &DeployMergeResult{
			Success: false,
			Error:   fmt.Errorf("deploy failed: %w", err).Error(),
			DiffResult: diffResult,
		}, err
	}

	d.logger.Info("Deploy merge completed successfully",
		"project", projectSlug,
		"source", sourceBranch,
		"target", targetBranch,
		"statements", len(diffResult.SQL),
	)

	return &DeployMergeResult{
		Success:      true,
		Message:      fmt.Sprintf("Deployed %d statements successfully", len(diffResult.SQL)),
		DiffResult:   diffResult,
		SnapshotName: snapshotDBName,
	}, nil
}

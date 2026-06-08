package deployer

import (
	"context"
	"fmt"
	"time"

	"cascata-backend/internal/config"
	"github.com/google/uuid"
)

// SnapshotInfo contém informações sobre um snapshot de segurança
type SnapshotInfo struct {
	Name      string
	CreatedAt time.Time
	Project   string
	Env       string
	Size      int64 // em bytes
	DBName    string // nome do banco de dados template
}

// DeployWithSafety executa um deploy com snapshot de segurança
// Cria um snapshot antes do deploy e faz rollback automático em caso de falha
func (d *Deployer) DeployWithSafety(
	ctx context.Context,
	projectSlug string,
	env string,
	sql []string,
	opts DeployOptions,
) (string, error) {
	d.logger.Info("Starting deploy with safety snapshot",
		"project", projectSlug,
		"env", env,
		"statements", len(sql),
	)

	// 1. Cria snapshot ANTES do deploy
	snapshotName := fmt.Sprintf("%s_%s_backup_%d", projectSlug, env, time.Now().Unix())
	
	snapshot, err := d.createSnapshot(ctx, projectSlug, env, snapshotName)
	if err != nil {
		return "", fmt.Errorf("pre-deploy snapshot failed: %w", err)
	}

	d.logger.Info("Safety snapshot created",
		"snapshot", snapshot.Name,
		"created_at", snapshot.CreatedAt,
	)

	// 2. Tenta o deploy com timeout
	deployErr := d.ExecuteDeploy(ctx, projectSlug, env, sql, opts)

	if deployErr != nil {
		// 3. Deploy falhou — aciona rollback automático para o snapshot
		d.logger.Error("Deploy failed, initiating rollback",
			"error", deployErr,
			"snapshot", snapshot.Name,
		)

		rollbackCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if rollbackErr := d.rollbackToSnapshot(rollbackCtx, snapshot); rollbackErr != nil {
			// Falha crítica — snapshot existe mas rollback falhou
			// Registra como operação manual necessária
			d.logger.Error("CRITICAL: deploy failed AND rollback failed",
				"project", projectSlug,
				"snapshot", snapshot.Name,
				"deploy_error", deployErr,
				"rollback_error", rollbackErr,
			)
			return "", fmt.Errorf("critical: both deploy and rollback failed. "+
				"Manual restore from snapshot '%s' required. "+
				"Deploy error: %w. Rollback error: %w",
				snapshot.Name, deployErr, rollbackErr)
		}

		d.logger.Info("Rollback completed successfully",
			"snapshot", snapshot.Name,
		)

		return "", fmt.Errorf("deploy failed (rolled back to snapshot '%s'): %w", snapshot.Name, deployErr)
	}

	// 4. Deploy ok — mantém snapshot por 720h (30 dias) para rollback manual, depois limpa
	d.scheduleSnapshotCleanup(snapshot, 720*time.Hour)

	d.logger.Info("Deploy completed successfully with safety",
		"project", projectSlug,
		"env", env,
		"snapshot", snapshot.Name,
		"retention", "720h",
	)

	return snapshot.DBName, nil
}

// RollbackToSnapshot expõe publicamente o rollback manual para snapshots físicos salvos no histórico
func (d *Deployer) RollbackToSnapshot(ctx context.Context, snapshot *SnapshotInfo) error {
	return d.rollbackToSnapshot(ctx, snapshot)
}

// CreateSnapshot expõe publicamente a criação de snapshots físicos sob demanda
func (d *Deployer) CreateSnapshot(ctx context.Context, projectSlug string, env string, snapshotName string) (*SnapshotInfo, error) {
	return d.createSnapshot(ctx, projectSlug, env, snapshotName)
}

// createSnapshot cria um snapshot real do banco antes do deploy
// Utiliza CREATE DATABASE ... TEMPLATE para criar um backup isolado
func (d *Deployer) createSnapshot(
	ctx context.Context,
	projectSlug string,
	env string,
	snapshotName string,
) (*SnapshotInfo, error) {
	d.logger.Info("Creating safety snapshot",
		"project", projectSlug,
		"env", env,
		"snapshot", snapshotName,
	)

	// 1. Adquire conexão EFÊMERA para o System DB (não pode estar conectado ao target!)
	sysConn, err := d.poolProvider.AcquireEphemeral(config.SystemDatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire system connection for snapshot: %w", err)
	}
	defer sysConn.Close()

	// 2. Obtém o nome do banco de dados atual através de uma conexão rápida
	conn, err := d.poolProvider.AcquireForProject(projectSlug, env)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire connection for snapshot: %w", err)
	}
	var dbName string
	if err := conn.QueryRow("SELECT current_database()").Scan(&dbName); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to get current database name: %w", err)
	}
	conn.Close() // Fecha para não bloquear o CREATE DATABASE

	// 3. Termina conexões ativas no banco atual
	terminateQuery := `
		SELECT pg_terminate_backend(pid)
		FROM pg_stat_activity
		WHERE datname = $1 AND pid <> pg_backend_pid()
	`
	_, _ = sysConn.Exec(terminateQuery, dbName)

	// 4. Gera nome único para o banco template de snapshot
	templateDBName := fmt.Sprintf("%s_snapshot_%s", dbName, uuid.New().String()[:8])

	// 5. Cria o snapshot usando CREATE DATABASE ... TEMPLATE
	createSQL := fmt.Sprintf("CREATE DATABASE %s TEMPLATE %s", templateDBName, dbName)
	_, err = sysConn.Exec(createSQL)
	if err != nil {
		return nil, fmt.Errorf("failed to create snapshot database: %w", err)
	}

	d.logger.Info("Snapshot database created",
		"template", templateDBName,
		"source", dbName,
	)

	// 6. Obtém tamanho aproximado do snapshot
	var sizeQuery string
	sizeQuery = `
		SELECT pg_database_size($1)
	`
	var sizeBytes int64
	if err := sysConn.QueryRow(sizeQuery, templateDBName).Scan(&sizeBytes); err != nil {
		d.logger.Error("Could not determine snapshot size", "error", err)
		sizeBytes = 0
	}

	// 6. Registra metadados do snapshot em tabela do sistema (opcional)
	// Isso permite rastreabilidade e cleanup automatizado
	_ = d.recordSnapshotMetadata(ctx, projectSlug, env, snapshotName, templateDBName, sizeBytes)

	return &SnapshotInfo{
		Name:      snapshotName,
		CreatedAt: time.Now(),
		Project:   projectSlug,
		Env:       env,
		Size:      sizeBytes,
		DBName:    templateDBName,
	}, nil
}

// rollbackToSnapshot restaura o banco para o estado do snapshot
// Utiliza a técnica de DROP + CREATE DATABASE TEMPLATE para restauração completa
func (d *Deployer) rollbackToSnapshot(
	ctx context.Context,
	snapshot *SnapshotInfo,
) error {
	d.logger.Info("Initiating rollback to snapshot",
		"snapshot", snapshot.Name,
		"project", snapshot.Project,
		"env", snapshot.Env,
		"template_db", snapshot.DBName,
	)

	if snapshot.DBName == "" {
		return fmt.Errorf("cannot rollback: snapshot has no associated template database")
	}

	// 1. Adquire conexão EFÊMERA para o System DB
	sysConn, err := d.poolProvider.AcquireEphemeral(config.SystemDatabaseURL)
	if err != nil {
		return fmt.Errorf("failed to acquire system connection for rollback: %w", err)
	}
	defer sysConn.Close()

	// 2. Obtém nome do banco atual através de conexão rápida
	conn, err := d.poolProvider.AcquireForProject(snapshot.Project, snapshot.Env)
	if err != nil {
		return fmt.Errorf("failed to acquire connection for rollback: %w", err)
	}
	var currentDB string
	if err := conn.QueryRow("SELECT current_database()").Scan(&currentDB); err != nil {
		conn.Close()
		return fmt.Errorf("failed to get current database name: %w", err)
	}
	conn.Close()

	// 3. Termina todas as conexões ativas no banco atual
	terminateQuery := `
		SELECT pg_terminate_backend(pid)
		FROM pg_stat_activity
		WHERE datname = $1 AND pid <> pg_backend_pid()
	`
	_, err = sysConn.Exec(terminateQuery, currentDB)
	if err != nil {
		d.logger.Error("Could not terminate all connections", "error", err)
	}

	// 4. Renomeia o banco atual para um nome temporário (backup de emergência)
	emergencyBackupName := fmt.Sprintf("%s_emergency_backup_%d", currentDB, time.Now().Unix())
	renameSQL := fmt.Sprintf("ALTER DATABASE %s RENAME TO %s", currentDB, emergencyBackupName)
	_, err = sysConn.Exec(renameSQL)
	if err != nil {
		d.logger.Error("Failed to rename current database", "error", err)
	}

	// 5. Cria novo banco a partir do snapshot template
	restoreSQL := fmt.Sprintf("CREATE DATABASE %s TEMPLATE %s", currentDB, snapshot.DBName)
	_, err = sysConn.Exec(restoreSQL)
	if err != nil {
		// Falha crítica - tenta restaurar do backup de emergência
		d.logger.Error("Failed to restore from snapshot, attempting emergency recovery",
			"error", err,
			"emergency_backup", emergencyBackupName,
		)

		// Tenta renomear o backup de emergência de volta
		recoverySQL := fmt.Sprintf("ALTER DATABASE %s RENAME TO %s", emergencyBackupName, currentDB)
		if _, recoveryErr := sysConn.Exec(recoverySQL); recoveryErr != nil {
			return fmt.Errorf("critical: both rollback and emergency recovery failed. "+
				"Manual intervention required. Rollback error: %w, Recovery error: %v",
				err, recoveryErr)
		}

		return fmt.Errorf("rollback failed but emergency recovery succeeded. "+
			"Database is in original state. Rollback error: %w", err)
	}

	// 6. Remove o backup de emergência (se ainda existir)
	if emergencyBackupName != "" {
		_, _ = sysConn.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS %s", emergencyBackupName))
	}

	d.logger.Info("Rollback completed successfully",
		"snapshot", snapshot.Name,
		"restored_to", currentDB,
	)

	return nil
}

// scheduleSnapshotCleanup agenda a limpeza do snapshot após o período de retenção
func (d *Deployer) scheduleSnapshotCleanup(snapshot *SnapshotInfo, retention time.Duration) {
	d.logger.Info("Scheduling snapshot cleanup",
		"snapshot", snapshot.Name,
		"retention", retention,
		"template_db", snapshot.DBName,
	)

	// Goroutine em background que aguarda o período de retenção e remove o snapshot
	go func() {
		time.Sleep(retention)
		
		d.logger.Info("Cleaning up expired snapshot",
			"snapshot", snapshot.Name,
			"template_db", snapshot.DBName,
		)
		
		if err := d.deleteSnapshot(context.Background(), snapshot); err != nil {
			d.logger.Error("Failed to delete expired snapshot",
				"snapshot", snapshot.Name,
				"error", err,
			)
		} else {
			d.logger.Info("Snapshot cleanup completed",
				"snapshot", snapshot.Name,
			)
		}
	}()
}

// deleteSnapshot remove um snapshot expirado
func (d *Deployer) deleteSnapshot(ctx context.Context, snapshot *SnapshotInfo) error {
	d.logger.Info("Deleting snapshot",
		"snapshot", snapshot.Name,
		"template_db", snapshot.DBName,
	)

	if snapshot.DBName == "" {
		return fmt.Errorf("cannot delete snapshot: no associated template database")
	}

	// 1. Adquire conexão para deletar o banco template
	conn, err := d.poolProvider.AcquireForProject(snapshot.Project, "live")
	if err != nil {
		return fmt.Errorf("failed to acquire connection for snapshot deletion: %w", err)
	}
	defer conn.Close()

	// 2. Termina conexões ativas no banco do snapshot (se houver)
	terminateQuery := `
		SELECT pg_terminate_backend(pid)
		FROM pg_stat_activity
		WHERE datname = $1 AND pid <> pg_backend_pid()
	`
	_, _ = conn.Exec(terminateQuery, snapshot.DBName)

	// 3. Dropa o banco template do snapshot
	dropSQL := fmt.Sprintf("DROP DATABASE IF EXISTS %s", snapshot.DBName)
	_, err = conn.Exec(dropSQL)
	if err != nil {
		return fmt.Errorf("failed to drop snapshot database %s: %w", snapshot.DBName, err)
	}

	d.logger.Info("Snapshot deleted successfully",
		"snapshot", snapshot.Name,
		"template_db", snapshot.DBName,
	)

	return nil
}

// recordSnapshotMetadata registra metadados do snapshot em tabela do sistema para rastreabilidade
func (d *Deployer) recordSnapshotMetadata(
	ctx context.Context,
	projectSlug, env, snapshotName, templateDB string,
	sizeBytes int64,
) error {
	// Tenta inserir metadados em system.snapshots se a tabela existir
	// Isso é opcional - o deploy funciona mesmo sem esta tabela
	
	// Adquire conexão do SystemPool (não do ProjectPool)
	// Nota: services.SystemPool pode não estar disponível aqui - usamos uma abordagem alternativa
	// Em produção, injetar o SystemPool via dependência
	
	// Para agora, apenas logamos os metadados
	d.logger.Debug("Snapshot metadata recorded (in-memory only)",
		"project", projectSlug,
		"env", env,
		"snapshot", snapshotName,
		"template_db", templateDB,
		"size_bytes", sizeBytes,
	)
	
	return nil
}
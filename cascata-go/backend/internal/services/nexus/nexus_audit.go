package nexus

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RecordExecution registra o log de execução no PostgreSQL.
// Esta função é compartilhada entre HookResolver (síncrono) e WorkerLane (assíncrono).
func RecordExecution(
	ctx context.Context,
	systemPool *pgxpool.Pool,
	logger *StructuredLogger,
	result *ExecutionResult,
	automationID string,
	tenantID string,
	triggerData map[string]interface{},
) {
	triggerJSON, _ := json.Marshal(triggerData)
	responseJSON, _ := json.Marshal(result.ResponseData)
	nodeResultsJSON, _ := json.Marshal(result.NodeResults)

	_, err := systemPool.Exec(ctx,
		`INSERT INTO system.nexus_execution_log
		 (trace_id, automation_id, tenant_id, graph_id, execution_mode, status,
		  duration_ms, nodes_executed, response_code, error_message,
		  trigger_data, response_data, node_results)
		 VALUES ($1, $2::uuid, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		result.TraceID,
		automationID,
		tenantID,
		result.GraphID,
		string(resultMode(result)),
		result.Status,
		result.DurationMs,
		result.NodesExecuted,
		result.ResponseCode,
		result.Error,
		triggerJSON,
		responseJSON,
		nodeResultsJSON,
	)
	if err != nil {
		logger.Warn("execution_log.insert_failed", map[string]interface{}{
			"trace_id": result.TraceID,
			"error":    err.Error(),
		})
	}

	step := 0
	for nodeID, nodeResult := range result.NodeResults {
		step++
		level := "info"
		message := "node completed"
		if nodeResult.Status == "error" {
			level = "error"
			message = "node failed"
		} else if nodeResult.Status == "skipped" {
			message = "node skipped"
		}

		outputJSON, _ := json.Marshal(nodeResult.Data)
		metaJSON, _ := json.Marshal(map[string]interface{}{
			"trace_id": result.TraceID,
			"status":   nodeResult.Status,
		})

		_, stepErr := systemPool.Exec(ctx,
			`INSERT INTO system.automation_step_logs
			 (execution_id, automation_id, project_slug, step_id, node_id, node_type, node_name,
			  level, message, input_data, output_data, error_details, duration_ms, metadata)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, '{}'::jsonb, $10, $11, $12, $13)`,
			result.TraceID,
			automationID,
			tenantID,
			fmt.Sprintf("%03d", step),
			nodeID,
			"", // node_type não está disponível no ExecutionResult facilmente aqui
			nodeID,
			level,
			message,
			outputJSON,
			nodeResult.Error,
			nodeResult.DurationMs,
			metaJSON,
		)
		if stepErr != nil {
			logger.Warn("step_log.insert_failed", map[string]interface{}{
				"trace_id": result.TraceID,
				"node_id":  nodeID,
				"error":    stepErr.Error(),
			})
		}
	}
}

// resultMode determina o modo de execução para o log.
func resultMode(result *ExecutionResult) ExecutionMode {
	// Se a duração for curta e síncrona no HookResolver, costuma ser FastLane.
	// No WorkerLane, o modo já vem no plano ou no resultado.
	return ExecutionMode(result.Status) // Fallback simplificado
}

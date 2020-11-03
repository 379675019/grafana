package ngalert

import (
	"context"

	"github.com/grafana/grafana/pkg/services/ngalert/eval"

	"github.com/go-macaron/binding"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/grafana/grafana/pkg/api"
	"github.com/grafana/grafana/pkg/api/routing"
	"github.com/grafana/grafana/pkg/middleware"
	"github.com/grafana/grafana/pkg/models"
	"github.com/grafana/grafana/pkg/setting"
	"github.com/grafana/grafana/pkg/tsdb"
	"github.com/grafana/grafana/pkg/util"
)

func (ng *AlertNG) registerAPIEndpoints() {
	ng.RouteRegister.Group("/api/alert-definitions", func(alertDefinitions routing.RouteRegister) {
		alertDefinitions.Get("", middleware.ReqSignedIn, api.Wrap(ng.ListAlertDefinitions))
		alertDefinitions.Get("/eval/:alertDefinitionId", validateOrgAlertDefinition, api.Wrap(ng.AlertDefinitionEval))
		alertDefinitions.Post("/eval", middleware.ReqSignedIn, binding.Bind(EvalAlertConditionCommand{}), api.Wrap(ng.ConditionEval))
		alertDefinitions.Get("/:alertDefinitionId", validateOrgAlertDefinition, api.Wrap(ng.GetAlertDefinitionEndpoint))
		alertDefinitions.Delete("/:alertDefinitionId", validateOrgAlertDefinition, api.Wrap(ng.DeleteAlertDefinitionEndpoint))
		alertDefinitions.Post("/", middleware.ReqSignedIn, binding.Bind(SaveAlertDefinitionCommand{}), api.Wrap(ng.CreateAlertDefinitionEndpoint))
		alertDefinitions.Put("/:alertDefinitionId", validateOrgAlertDefinition, binding.Bind(UpdateAlertDefinitionCommand{}), api.Wrap(ng.UpdateAlertDefinitionEndpoint))
	})
}

// ConditionEval handles POST /api/alert-definitions/eval.
func (ng *AlertNG) ConditionEval(c *models.ReqContext, dto EvalAlertConditionCommand) api.Response {
	alertCtx, cancelFn := context.WithTimeout(context.Background(), setting.AlertingEvaluationTimeout)
	defer cancelFn()

	alertExecCtx := eval.AlertExecCtx{Ctx: alertCtx, SignedInUser: c.SignedInUser}

	fromStr := c.Query("from")
	if fromStr == "" {
		fromStr = "now-3h"
	}

	toStr := c.Query("to")
	if toStr == "" {
		toStr = "now"
	}

	execResult, err := dto.Condition.Execute(alertExecCtx, fromStr, toStr)
	if err != nil {
		return api.Error(400, "Failed to execute conditions", err)
	}

	evalResults, err := eval.EvaluateExecutionResult(execResult)
	if err != nil {
		return api.Error(400, "Failed to evaluate results", err)
	}

	frame := evalResults.AsDataFrame()
	df := tsdb.NewDecodedDataFrames([]*data.Frame{&frame})
	instances, err := df.Encoded()
	if err != nil {
		return api.Error(400, "Failed to encode result dataframes", err)
	}

	return api.JSON(200, util.DynMap{
		"instances": instances,
	})
}

// §AlertDefinitionEval handles GET /api/alert-definitions/eval/:dashboardId/:panelId/:refId".
func (ng *AlertNG) AlertDefinitionEval(c *models.ReqContext) api.Response {
	alertDefinitionID := c.ParamsInt64(":alertDefinitionId")

	fromStr := c.Query("from")
	if fromStr == "" {
		fromStr = "now-3h"
	}

	toStr := c.Query("to")
	if toStr == "" {
		toStr = "now"
	}

	conditions, err := ng.LoadAlertCondition(alertDefinitionID, c.SignedInUser, c.SkipCache)
	if err != nil {
		return api.Error(400, "Failed to load conditions", err)
	}

	alertCtx, cancelFn := context.WithTimeout(context.Background(), setting.AlertingEvaluationTimeout)
	defer cancelFn()

	alertExecCtx := eval.AlertExecCtx{Ctx: alertCtx, SignedInUser: c.SignedInUser}

	execResult, err := conditions.Execute(alertExecCtx, fromStr, toStr)
	if err != nil {
		return api.Error(400, "Failed to execute conditions", err)
	}

	evalResults, err := eval.EvaluateExecutionResult(execResult)
	if err != nil {
		return api.Error(400, "Failed to evaluate results", err)
	}

	frame := evalResults.AsDataFrame()

	df := tsdb.NewDecodedDataFrames([]*data.Frame{&frame})
	instances, err := df.Encoded()
	if err != nil {
		return api.Error(400, "Failed to encode result dataframes", err)
	}

	return api.JSON(200, util.DynMap{
		"instances": instances,
	})
}

// GetAlertDefinitionEndpoint handles GET /api/alert-definitions/:alertDefinitionId.
func (ng *AlertNG) GetAlertDefinitionEndpoint(c *models.ReqContext) api.Response {
	alertDefinitionID := c.ParamsInt64(":alertDefinitionId")

	query := GetAlertDefinitionByIDQuery{
		ID: alertDefinitionID,
	}

	if err := ng.Bus.Dispatch(&query); err != nil {
		return api.Error(500, "Failed to get alert definition", err)
	}

	return api.JSON(200, &query.Result)
}

// DeleteAlertDefinitionEndpoint handles DELETE /api/alert-definitions/:alertDefinitionId.
func (ng *AlertNG) DeleteAlertDefinitionEndpoint(c *models.ReqContext) api.Response {
	alertDefinitionID := c.ParamsInt64(":alertDefinitionId")

	query := DeleteAlertDefinitionByIDQuery{
		ID:    alertDefinitionID,
		OrgID: c.SignedInUser.OrgId,
	}

	if err := ng.DeleteAlertDefinitionByID(&query); err != nil {
		return api.Error(500, "Failed to delete alert definition", err)
	}

	return api.JSON(200, util.DynMap{"affectedRows": query.RowsAffected})
}

// UpdateAlertDefinitionEndpoint handles PUT /api/alert-definitions/:alertDefinitionId.
func (ng *AlertNG) UpdateAlertDefinitionEndpoint(c *models.ReqContext, cmd UpdateAlertDefinitionCommand) api.Response {
	cmd.ID = c.ParamsInt64(":alertDefinitionId")
	cmd.SignedInUser = c.SignedInUser
	cmd.SkipCache = c.SkipCache

	if err := ng.UpdateAlertDefinition(&cmd); err != nil {
		return api.Error(500, "Failed to update alert definition", err)
	}

	return api.JSON(200, util.DynMap{"affectedRows": cmd.RowsAffected, "id": cmd.Result.Id})
}

// CreateAlertDefinitionEndpoint handles POST /api/alert-definitions.
func (ng *AlertNG) CreateAlertDefinitionEndpoint(c *models.ReqContext, cmd SaveAlertDefinitionCommand) api.Response {
	cmd.OrgID = c.SignedInUser.OrgId
	cmd.SignedInUser = c.SignedInUser
	cmd.SkipCache = c.SkipCache

	if err := ng.SaveAlertDefinition(&cmd); err != nil {
		return api.Error(500, "Failed to create alert definition", err)
	}

	return api.JSON(200, util.DynMap{"id": cmd.Result.Id})
}

// ListAlertDefinitions handles GET /api/alert-definitions.
func (ng *AlertNG) ListAlertDefinitions(c *models.ReqContext) api.Response {
	cmd := ListAlertDefinitionsCommand{OrgID: c.SignedInUser.OrgId}

	if err := ng.GetAlertDefinitions(&cmd); err != nil {
		return api.Error(500, "Failed to list alert definitions", err)
	}

	return api.JSON(200, util.DynMap{"results": cmd.Result})
}

package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"fermentation-kinetics-deviation-analysis/backend/internal/algorithm"
	"fermentation-kinetics-deviation-analysis/backend/internal/constants"
	"fermentation-kinetics-deviation-analysis/backend/internal/dto"
	"fermentation-kinetics-deviation-analysis/backend/internal/model"
	"fermentation-kinetics-deviation-analysis/backend/internal/repository"
	"fermentation-kinetics-deviation-analysis/backend/internal/timeseries"
	"fermentation-kinetics-deviation-analysis/backend/internal/util"
)

func TestAnalysisIdempotencyReviewerSeparationAndReplay(t *testing.T) {
	db := newTestDB(t)
	vesselRepo := repository.NewFermentationVesselRepository(db)
	recipeRepo := repository.NewCultureRecipeRepository(db)
	seriesRepo := repository.NewSensorSeriesRepository(db)
	analysisRepo := repository.NewDeviationAnalysisRepository(db)
	auditRepo := repository.NewAuditRepository(db)
	now := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	vessel := model.FermentationVessel{
		VesselCode: "FV-A1", Name: "Analysis vessel", WorkingVolumeL: 100,
		SensorChannels: `["ph","temperature"]`, Location: "Lab", OwnerTeam: "Process",
		VesselState: "active", CommissionedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := vesselRepo.Create(context.Background(), &vessel); err != nil {
		t.Fatal(err)
	}
	boundaries, references, tolerances := testRecipeConfig(t)
	recipe := model.CultureRecipe{
		VesselID: vessel.ID, RecipeCode: "ANALYSIS-A", Version: 1, Organism: "Test organism",
		TargetDurationH: 8, PhaseBoundariesJSON: string(boundaries), ReferenceCurvesJSON: string(references),
		ToleranceProfileJSON: string(tolerances), RecipeState: "published",
		CreatedBy: 8, CreatedByName: "scientist", CreatedAt: now, UpdatedAt: now,
	}
	if err := recipeRepo.Create(context.Background(), &recipe); err != nil {
		t.Fatal(err)
	}
	points := make([]timeseries.Point, 0, 9)
	for hour := 0; hour <= 8; hour++ {
		ph := 7 - float64(hour)*0.05
		temperature := 29.5 + 0.04*float64(hour)
		points = append(points, timeseries.Point{
			Timestamp: now.Add(time.Duration(hour) * time.Hour),
			Values:    map[string]*float64{"ph": &ph, "temperature": &temperature},
		})
	}
	pointsJSON, err := timeseries.EncodePoints(points)
	if err != nil {
		t.Fatal(err)
	}
	series := model.SensorSeries{
		VesselID: vessel.ID, RecipeID: recipe.ID, RunCode: "RUN-A1", Channel: "multichannel",
		SampleIntervalS: 3600, PointsJSON: pointsJSON, StartedAt: now, EndedAt: now.Add(8 * time.Hour),
		SourceChecksum: util.HashString(pointsJSON), SeriesState: "ready", QualitySummary: `{"valid":true}`,
		NormalizationJSON: `{"method":"median_iqr"}`, ImportedBy: 9, ImportedByName: "analyst",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := seriesRepo.Create(context.Background(), &series); err != nil {
		t.Fatal(err)
	}
	svc := NewDeviationAnalysisService(analysisRepo, recipeRepo, seriesRepo, auditRepo, algorithm.NewEvaluator())
	initiator := util.Actor{UserID: 9, Username: "analyst", Role: "data_analyst", RequestID: "req-run"}
	first, reused, err := svc.Run(context.Background(), dto.RunDeviationAnalysisRequest{SensorSeriesID: series.ID}, "idem-a", initiator)
	if err != nil || reused {
		t.Fatalf("first run reused=%v err=%v", reused, err)
	}
	second, reused, err := svc.Run(context.Background(), dto.RunDeviationAnalysisRequest{SensorSeriesID: series.ID}, "idem-a", initiator)
	if err != nil || !reused || second.ID != first.ID {
		t.Fatalf("same-key run id=%d reused=%v err=%v", second.ID, reused, err)
	}
	third, reused, err := svc.Run(context.Background(), dto.RunDeviationAnalysisRequest{SensorSeriesID: series.ID}, "idem-b", initiator)
	if err != nil || !reused || third.ID != first.ID {
		t.Fatalf("same-input run id=%d reused=%v err=%v", third.ID, reused, err)
	}
	reviewer := util.Actor{UserID: 10, Username: "reviewer", Role: "reviewer", RequestID: "req-review"}
	if _, err := svc.Transition(context.Background(), first.ID, dto.DeviationAnalysisTransitionRequest{
		ToState: "reviewed", Comment: "Evidence reviewed.",
	}, reviewer); err != nil {
		t.Fatalf("review transition: %v", err)
	}
	_, err = svc.Transition(context.Background(), first.ID, dto.DeviationAnalysisTransitionRequest{
		ToState: "confirmed", Comment: "Self confirmation must fail.",
	}, initiator)
	var appErr *util.AppError
	if !errors.As(err, &appErr) || appErr.Code != util.CodeReviewerConflict {
		t.Fatalf("self-confirm error=%v, want reviewer conflict", err)
	}
	if _, err := svc.Transition(context.Background(), first.ID, dto.DeviationAnalysisTransitionRequest{
		ToState: "confirmed", Comment: "Independent confirmation.",
	}, reviewer); err != nil {
		t.Fatalf("independent confirm: %v", err)
	}
	replayed, err := svc.Replay(context.Background(), first.ID, reviewer)
	if err != nil || replayed.ReplayVerified == nil || !*replayed.ReplayVerified {
		t.Fatalf("replay verified=%v err=%v", replayed.ReplayVerified, err)
	}
}

func TestRunIsolatesChannelAndPersistsDeterministicReport(t *testing.T) {
	db := newTestDB(t)
	vesselRepo := repository.NewFermentationVesselRepository(db)
	recipeRepo := repository.NewCultureRecipeRepository(db)
	seriesRepo := repository.NewSensorSeriesRepository(db)
	analysisRepo := repository.NewDeviationAnalysisRepository(db)
	auditRepo := repository.NewAuditRepository(db)
	now := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	vessel := model.FermentationVessel{
		VesselCode: "FV-A2", Name: "Isolation vessel", WorkingVolumeL: 100,
		SensorChannels: `["ph","temperature","do"]`, Location: "Lab", OwnerTeam: "Process",
		VesselState: "active", CommissionedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := vesselRepo.Create(context.Background(), &vessel); err != nil {
		t.Fatal(err)
	}
	boundaries, err := json.Marshal([]algorithm.PhaseBoundary{
		{Phase: constants.PhaseLag, StartHour: 0, EndHour: 2},
		{Phase: constants.PhaseGrowth, StartHour: 2, EndHour: 4},
		{Phase: constants.PhaseProduction, StartHour: 4, EndHour: 6},
		{Phase: constants.PhaseHarvest, StartHour: 6, EndHour: 8},
	})
	if err != nil {
		t.Fatal(err)
	}
	curves := map[string][]algorithm.CurvePoint{"ph": {}, "temperature": {}, "do": {}}
	for hour := 0; hour <= 8; hour++ {
		curves["ph"] = append(curves["ph"], algorithm.CurvePoint{ElapsedHour: float64(hour), Value: 7 - 0.05*float64(hour)})
		curves["temperature"] = append(curves["temperature"], algorithm.CurvePoint{ElapsedHour: float64(hour), Value: 29.5 + 0.04*float64(hour)})
		curves["do"] = append(curves["do"], algorithm.CurvePoint{ElapsedHour: float64(hour), Value: 68 - 1.4*float64(hour)})
	}
	references, err := json.Marshal(curves)
	if err != nil {
		t.Fatal(err)
	}
	tolerances, err := json.Marshal(map[string]algorithm.ChannelTolerance{
		"ph": {Weight: 1, MaxDistance: 1}, "temperature": {Weight: 1, MaxDistance: 1},
		"do": {Weight: 1, MaxDistance: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	recipe := model.CultureRecipe{
		VesselID: vessel.ID, RecipeCode: "ANALYSIS-ISO", Version: 1, Organism: "Test organism",
		TargetDurationH: 8, PhaseBoundariesJSON: string(boundaries), ReferenceCurvesJSON: string(references),
		ToleranceProfileJSON: string(tolerances), RecipeState: "published",
		CreatedBy: 8, CreatedByName: "scientist", CreatedAt: now, UpdatedAt: now,
	}
	if err := recipeRepo.Create(context.Background(), &recipe); err != nil {
		t.Fatal(err)
	}
	// do is absent at hours 1, 5 and 8 (33.3% missing); ph and temperature are complete.
	missingDO := map[int]bool{1: true, 5: true, 8: true}
	points := make([]timeseries.Point, 0, 9)
	for hour := 0; hour <= 8; hour++ {
		ph := 7 - 0.05*float64(hour)
		temperature := 29.5 + 0.04*float64(hour)
		oxygen := 68 - 1.4*float64(hour)
		values := map[string]*float64{"ph": &ph, "temperature": &temperature}
		if !missingDO[hour] {
			values["do"] = &oxygen
		}
		points = append(points, timeseries.Point{
			Timestamp: now.Add(time.Duration(hour) * time.Hour), Values: values,
		})
	}
	pointsJSON, err := timeseries.EncodePoints(points)
	if err != nil {
		t.Fatal(err)
	}
	series := model.SensorSeries{
		VesselID: vessel.ID, RecipeID: recipe.ID, RunCode: "RUN-ISO-1", Channel: "multichannel",
		SampleIntervalS: 3600, PointsJSON: pointsJSON, StartedAt: now, EndedAt: now.Add(8 * time.Hour),
		SourceChecksum: util.HashString(pointsJSON), SeriesState: "ready", QualitySummary: `{"valid":true}`,
		NormalizationJSON: `{"method":"median_iqr"}`, ImportedBy: 9, ImportedByName: "analyst",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := seriesRepo.Create(context.Background(), &series); err != nil {
		t.Fatal(err)
	}
	svc := NewDeviationAnalysisService(analysisRepo, recipeRepo, seriesRepo, auditRepo, algorithm.NewEvaluator())
	actor := util.Actor{UserID: 9, Username: "analyst", Role: "data_analyst", RequestID: "req-iso"}
	first, reused, err := svc.Run(context.Background(), dto.RunDeviationAnalysisRequest{SensorSeriesID: series.ID}, "idem-iso", actor)
	if err != nil || reused {
		t.Fatalf("isolated run reused=%v err=%v", reused, err)
	}
	if first.AnalysisState != "completed" {
		t.Fatalf("state=%s failure=%s", first.AnalysisState, first.FailureReason)
	}
	if len(first.IsolationJSON) == 0 || string(first.IsolationJSON) == "null" {
		t.Fatalf("isolation evidence was not persisted: %s", first.IsolationJSON)
	}
	var isolation algorithm.IsolationReport
	if err := json.Unmarshal(first.IsolationJSON, &isolation); err != nil {
		t.Fatalf("decode isolation report: %v", err)
	}
	if !isolation.Isolated || len(isolation.IsolatedChannels) != 1 || isolation.IsolatedChannels[0].Channel != "do" {
		t.Fatalf("unexpected isolation report: %+v", isolation)
	}
	// Same input replays through the frozen snapshot and must agree exactly.
	replayed, err := svc.Replay(context.Background(), first.ID, util.Actor{
		UserID: 10, Username: "reviewer", Role: "reviewer", RequestID: "req-iso-replay",
	})
	if err != nil || replayed.ReplayVerified == nil || !*replayed.ReplayVerified {
		t.Fatalf("replay verified=%v err=%v", replayed.ReplayVerified, err)
	}
}

func TestRunRequiresReadySeriesAndIdempotencyKey(t *testing.T) {
	db := newTestDB(t)
	svc := NewDeviationAnalysisService(
		repository.NewDeviationAnalysisRepository(db), repository.NewCultureRecipeRepository(db),
		repository.NewSensorSeriesRepository(db), repository.NewAuditRepository(db), algorithm.NewEvaluator(),
	)
	_, _, err := svc.Run(context.Background(), dto.RunDeviationAnalysisRequest{SensorSeriesID: 99}, "", util.Actor{})
	var appErr *util.AppError
	if !errors.As(err, &appErr) || appErr.Code != util.CodeIdempotency {
		t.Fatalf("missing key error=%v", err)
	}
}

func TestAnalysisRoleContract(t *testing.T) {
	if constants.HasPermission(constants.RoleDataAnalyst, constants.PermissionAnalysisConfirm) {
		t.Fatal("data analyst should not receive confirm permission")
	}
}

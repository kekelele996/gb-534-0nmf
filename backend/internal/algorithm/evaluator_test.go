package algorithm

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"fermentation-kinetics-deviation-analysis/backend/internal/constants"
	"fermentation-kinetics-deviation-analysis/backend/internal/model"
	"fermentation-kinetics-deviation-analysis/backend/internal/timeseries"
	"fermentation-kinetics-deviation-analysis/backend/internal/util"
)

func TestDTWDeterministicFixture(t *testing.T) {
	distance, path, err := DTW([]float64{1, 2, 3, 4}, []float64{1, 2, 3, 4}, 2)
	if err != nil {
		t.Fatalf("DTW: %v", err)
	}
	if distance != 0 || len(path) != 4 {
		t.Fatalf("distance=%v path=%v", distance, path)
	}
}

func TestEvaluatorIsDeterministicAndReplayable(t *testing.T) {
	snapshot := evaluatorFixture(t)
	first, err := NewEvaluator().Evaluate(snapshot)
	if err != nil {
		t.Fatalf("first evaluate: %v", err)
	}
	second, err := NewEvaluator().Evaluate(snapshot)
	if err != nil {
		t.Fatalf("second evaluate: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("results differ:\nfirst=%+v\nsecond=%+v", first, second)
	}
	if first.DeviationLevel != constants.DeviationNormal {
		t.Fatalf("identical fixture level=%s, want normal", first.DeviationLevel)
	}
	var isolation IsolationReport
	if err := json.Unmarshal([]byte(first.IsolationJSON), &isolation); err != nil {
		t.Fatalf("decode isolation report: %v", err)
	}
	if isolation.Isolated {
		t.Fatalf("complete fixture should not isolate channels: %+v", isolation)
	}
	encoded, err := snapshot.Canonical()
	if err != nil {
		t.Fatalf("canonical snapshot: %v", err)
	}
	decoded, err := DecodeSnapshot(encoded)
	if err != nil || !reflect.DeepEqual(snapshot, decoded) {
		t.Fatalf("snapshot replay mismatch: %v", err)
	}
}

func TestLegacySnapshotReplaysWithoutIsolation(t *testing.T) {
	snapshot := evaluatorFixture(t)
	snapshot.AlgorithmVersion = LegacyVersion
	result, err := NewEvaluator().Evaluate(snapshot)
	if err != nil {
		t.Fatalf("legacy evaluate: %v", err)
	}
	if result.IsolationJSON != "" {
		t.Fatalf("legacy result must not carry isolation evidence, got %q", result.IsolationJSON)
	}
}

func TestChannelIsolationRecomputesWeightsAndIsDeterministic(t *testing.T) {
	snapshot := isolationFixture(t, map[string][]int{"ph": {}, "temperature": {}, "do": {1, 5, 8}})
	first, err := NewEvaluator().Evaluate(snapshot)
	if err != nil {
		t.Fatalf("evaluate with isolated channel: %v", err)
	}
	second, err := NewEvaluator().Evaluate(snapshot)
	if err != nil {
		t.Fatalf("replay with isolated channel: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("isolated results differ:\nfirst=%+v\nsecond=%+v", first, second)
	}

	var isolation IsolationReport
	if err := json.Unmarshal([]byte(first.IsolationJSON), &isolation); err != nil {
		t.Fatalf("decode isolation report: %v", err)
	}
	if !isolation.Isolated {
		t.Fatal("expected an isolation event")
	}
	if len(isolation.IsolatedChannels) != 1 || isolation.IsolatedChannels[0].Channel != "do" {
		t.Fatalf("isolated channels=%+v, want do only", isolation.IsolatedChannels)
	}
	if isolation.IsolatedChannels[0].MissingRate <= IsolationMaxMissingRate {
		t.Fatalf("missing rate %v must exceed %.2f", isolation.IsolatedChannels[0].MissingRate, IsolationMaxMissingRate)
	}
	if isolation.IsolatedChannels[0].WeightAfter != 0 || isolation.IsolatedChannels[0].WeightBefore <= 0 {
		t.Fatalf("isolated channel weight before=%v after=%v", isolation.IsolatedChannels[0].WeightBefore, isolation.IsolatedChannels[0].WeightAfter)
	}
	if !reflect.DeepEqual(isolation.EffectiveChannels, []string{"ph", "temperature"}) {
		t.Fatalf("effective channels=%v", isolation.EffectiveChannels)
	}
	if len(isolation.AffectedPhases) != 4 {
		t.Fatalf("affected phases=%v, want all four phases", isolation.AffectedPhases)
	}
	for _, phase := range isolation.AffectedPhases {
		if !reflect.DeepEqual(phase.IsolatedChannels, []string{"do"}) {
			t.Fatalf("phase %s isolated channels=%v", phase.Phase, phase.IsolatedChannels)
		}
		if phase.WeightAfter >= phase.WeightBefore {
			t.Fatalf("phase %s weight did not drop: before=%v after=%v", phase.Phase, phase.WeightBefore, phase.WeightAfter)
		}
		if phase.WeightReduction <= 0 || phase.WeightReduction > 1 {
			t.Fatalf("phase %s reduction=%v", phase.Phase, phase.WeightReduction)
		}
	}
	if isolation.OverallAfter != first.OverallScore {
		t.Fatalf("overall after=%v must equal result score=%v", isolation.OverallAfter, first.OverallScore)
	}

	var scores []PhaseEvidence
	if err := json.Unmarshal([]byte(first.PhaseScoresJSON), &scores); err != nil {
		t.Fatalf("decode phase scores: %v", err)
	}
	for _, phase := range scores {
		if _, present := phase.ChannelScores["do"]; present {
			t.Fatalf("phase %s still scores isolated channel do: %+v", phase.Phase, phase.ChannelScores)
		}
		if _, present := phase.ChannelScores["ph"]; !present {
			t.Fatalf("phase %s lost surviving channel ph: %+v", phase.Phase, phase.ChannelScores)
		}
	}
}

func TestIsolationRejectsWhenFewerThanTwoEffectiveChannels(t *testing.T) {
	snapshot := isolationFixture(t, map[string][]int{"ph": {}, "temperature": {1, 5, 8}})
	_, err := NewEvaluator().Evaluate(snapshot)
	if err == nil || !strings.Contains(err.Error(), "effective channels") {
		t.Fatalf("error=%v, want effective channel rejection", err)
	}
}

func TestIsolationRejectsWhenPhaseLosesAllUsableChannels(t *testing.T) {
	snapshot := phaseStarvationFixture(t)
	_, err := NewEvaluator().Evaluate(snapshot)
	if err == nil || !strings.Contains(err.Error(), "phase") {
		t.Fatalf("error=%v, want unusable-phase rejection", err)
	}
}

func evaluatorFixture(t *testing.T) Snapshot {
	t.Helper()
	return isolationFixture(t, map[string][]int{"ph": {}, "temperature": {}})
}

// isolationFixture builds an hourly 0..8h, four-phase recipe where the actual
// trajectory equals every reference curve. The missingHours map lists the
// timestamps (in whole hours) at which each channel is absent.
func isolationFixture(t *testing.T, missingHours map[string][]int) Snapshot {
	t.Helper()
	started := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	channels := make([]string, 0, len(missingHours))
	for channel := range missingHours {
		channels = append(channels, channel)
	}
	missingIndex := map[string]map[int]bool{}
	for _, channel := range channels {
		gaps := map[int]bool{}
		for _, hour := range missingHours[channel] {
			gaps[hour] = true
		}
		missingIndex[channel] = gaps
	}
	points := make([]timeseries.Point, 0, 9)
	reference := map[string][]CurvePoint{}
	for _, channel := range channels {
		reference[channel] = []CurvePoint{}
	}
	for hour := 0; hour <= 8; hour++ {
		values := map[string]*float64{}
		for _, channel := range channels {
			value := referenceValue(channel, float64(hour))
			if !missingIndex[channel][hour] {
				values[channel] = &value
			}
		}
		points = append(points, timeseries.Point{
			Timestamp: started.Add(time.Duration(hour) * time.Hour), Values: values,
		})
		for _, channel := range channels {
			reference[channel] = append(reference[channel], CurvePoint{
				ElapsedHour: float64(hour), Value: referenceValue(channel, float64(hour)),
			})
		}
	}
	pointsJSON, err := timeseries.EncodePoints(points)
	if err != nil {
		t.Fatal(err)
	}
	boundaries, _ := util.CanonicalJSON([]PhaseBoundary{
		{Phase: constants.PhaseLag, StartHour: 0, EndHour: 2},
		{Phase: constants.PhaseGrowth, StartHour: 2, EndHour: 4},
		{Phase: constants.PhaseProduction, StartHour: 4, EndHour: 6},
		{Phase: constants.PhaseHarvest, StartHour: 6, EndHour: 8},
	})
	references, _ := util.CanonicalJSON(reference)
	tolerancesMap := map[string]ChannelTolerance{}
	for _, channel := range channels {
		tolerancesMap[channel] = ChannelTolerance{Weight: 1, MaxDistance: 1}
	}
	tolerances, _ := util.CanonicalJSON(tolerancesMap)
	series := model.SensorSeries{
		ID: 3, VesselID: 2, RecipeID: 4, RunCode: "FIXTURE",
		Channel: "multichannel", SampleIntervalS: 3600, PointsJSON: pointsJSON,
		SourceChecksum: util.HashString(pointsJSON),
		StartedAt: started, EndedAt: started.Add(8 * time.Hour),
	}
	recipe := model.CultureRecipe{
		ID: 4, Version: 2, TargetDurationH: 24, PhaseBoundariesJSON: boundaries,
		ReferenceCurvesJSON: references, ToleranceProfileJSON: tolerances,
	}
	return NewSnapshot(series, recipe)
}

// phaseStarvationFixture isolates do (~20% gaps) while ph and temperature each
// keep only one lag observation, leaving the lag phase without any comparable
// channel after isolation.
func phaseStarvationFixture(t *testing.T) Snapshot {
	t.Helper()
	started := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	channels := []string{"ph", "temperature", "do"}
	points := make([]timeseries.Point, 0, 49)
	reference := map[string][]CurvePoint{}
	for _, channel := range channels {
		reference[channel] = []CurvePoint{}
	}
	for step := 0; step <= 48; step++ {
		hour := float64(step) / 2
		values := map[string]*float64{}
		for _, channel := range channels {
			value := referenceValue(channel, hour)
			switch {
			case channel == "do" && step%5 == 1: // ~20.4% missing across the run
			case (channel == "ph" || channel == "temperature") && hour < 4: // only the 4h lag boundary survives
				continue
			default:
				values[channel] = &value
			}
		}
		points = append(points, timeseries.Point{
			Timestamp: started.Add(time.Duration(hour * float64(time.Hour))), Values: values,
		})
		for _, channel := range channels {
			reference[channel] = append(reference[channel], CurvePoint{
				ElapsedHour: hour, Value: referenceValue(channel, hour),
			})
		}
	}
	pointsJSON, err := timeseries.EncodePoints(points)
	if err != nil {
		t.Fatal(err)
	}
	boundaries, _ := util.CanonicalJSON([]PhaseBoundary{
		{Phase: constants.PhaseLag, StartHour: 0, EndHour: 4},
		{Phase: constants.PhaseGrowth, StartHour: 4, EndHour: 10},
		{Phase: constants.PhaseProduction, StartHour: 10, EndHour: 20},
		{Phase: constants.PhaseHarvest, StartHour: 20, EndHour: 24},
	})
	references, _ := util.CanonicalJSON(reference)
	tolerancesMap := map[string]ChannelTolerance{}
	for _, channel := range channels {
		tolerancesMap[channel] = ChannelTolerance{Weight: 1, MaxDistance: 1}
	}
	tolerances, _ := util.CanonicalJSON(tolerancesMap)
	series := model.SensorSeries{
		ID: 5, VesselID: 2, RecipeID: 6, RunCode: "FIXTURE-STARVE",
		Channel: "multichannel", SampleIntervalS: 1800, PointsJSON: pointsJSON,
		SourceChecksum: util.HashString(pointsJSON),
		StartedAt: started, EndedAt: started.Add(24 * time.Hour),
	}
	recipe := model.CultureRecipe{
		ID: 6, Version: 1, TargetDurationH: 24, PhaseBoundariesJSON: boundaries,
		ReferenceCurvesJSON: references, ToleranceProfileJSON: tolerances,
	}
	return NewSnapshot(series, recipe)
}

func referenceValue(channel string, hour float64) float64 {
	switch channel {
	case "ph":
		return 7.0 - 0.05*hour
	case "temperature":
		return 29.5 + 0.04*hour
	default:
		return 68 - 1.4*hour
	}
}

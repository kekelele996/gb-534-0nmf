package algorithm

import (
	"encoding/json"
	"reflect"
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
	encoded, err := snapshot.Canonical()
	if err != nil {
		t.Fatalf("canonical snapshot: %v", err)
	}
	decoded, err := DecodeSnapshot(encoded)
	if err != nil || !reflect.DeepEqual(snapshot, decoded) {
		t.Fatalf("snapshot replay mismatch: %v", err)
	}
}

func evaluatorFixture(t *testing.T) Snapshot {
	t.Helper()
	started := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	points := make([]timeseries.Point, 0, 9)
	reference := map[string][]CurvePoint{"ph": {}, "temperature": {}}
	for hour := 0; hour <= 8; hour++ {
		phValue := 7.0 - float64(hour)*0.05
		phCopy := phValue
		tempValue := 30.0 + float64(hour)*0.1
		tempCopy := tempValue
		points = append(points, timeseries.Point{
			Timestamp: started.Add(time.Duration(hour) * time.Hour),
			Values:    map[string]*float64{"ph": &phCopy, "temperature": &tempCopy},
		})
		reference["ph"] = append(reference["ph"], CurvePoint{ElapsedHour: float64(hour), Value: phValue})
		reference["temperature"] = append(reference["temperature"], CurvePoint{ElapsedHour: float64(hour), Value: tempValue})
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
	tolerances, _ := util.CanonicalJSON(map[string]ChannelTolerance{
		"ph":          {Weight: 1, MaxDistance: 1},
		"temperature": {Weight: 1, MaxDistance: 1},
	})
	series := model.SensorSeries{
		ID: 3, VesselID: 2, RecipeID: 4, RunCode: "FIXTURE", Channel: "multichannel",
		SampleIntervalS: 3600, PointsJSON: pointsJSON, SourceChecksum: util.HashString(pointsJSON),
		StartedAt: started, EndedAt: started.Add(8 * time.Hour),
	}
	recipe := model.CultureRecipe{
		ID: 4, Version: 2, TargetDurationH: 8, PhaseBoundariesJSON: boundaries,
		ReferenceCurvesJSON: references, ToleranceProfileJSON: tolerances,
	}
	return NewSnapshot(series, recipe)
}

func isolationFixture(t *testing.T, channels map[string][]float64) Snapshot {
	t.Helper()
	started := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	points := make([]timeseries.Point, 0, 11)
	for hour := 0; hour <= 10; hour++ {
		values := map[string]*float64{}
		for channel, series := range channels {
			if value := series[hour]; !isMissingSentinel(value) {
				copyValue := value
				values[channel] = &copyValue
			}
		}
		points = append(points, timeseries.Point{
			Timestamp: started.Add(time.Duration(hour) * time.Hour), Values: values,
		})
	}
	pointsJSON, err := timeseries.EncodePoints(points)
	if err != nil {
		t.Fatal(err)
	}
	reference := map[string][]CurvePoint{}
	tolerances := map[string]ChannelTolerance{}
	for channel, series := range channels {
		for hour, value := range series {
			reference[channel] = append(reference[channel], CurvePoint{
				ElapsedHour: float64(hour), Value: cleanReferenceValue(channel, hour, value),
			})
		}
		tolerances[channel] = ChannelTolerance{Weight: 1, MaxDistance: 1}
	}
	boundaries, _ := util.CanonicalJSON([]PhaseBoundary{
		{Phase: constants.PhaseLag, StartHour: 0, EndHour: 2},
		{Phase: constants.PhaseGrowth, StartHour: 2, EndHour: 5},
		{Phase: constants.PhaseProduction, StartHour: 5, EndHour: 8},
		{Phase: constants.PhaseHarvest, StartHour: 8, EndHour: 10},
	})
	references, _ := util.CanonicalJSON(reference)
	toleranceJSON, _ := util.CanonicalJSON(tolerances)
	series := model.SensorSeries{
		ID: 5, VesselID: 2, RecipeID: 6, RunCode: "ISOLATION", Channel: "multichannel",
		SampleIntervalS: 3600, PointsJSON: pointsJSON, SourceChecksum: util.HashString(pointsJSON),
		StartedAt: started, EndedAt: started.Add(10 * time.Hour),
	}
	recipe := model.CultureRecipe{
		ID: 6, Version: 1, TargetDurationH: 10, PhaseBoundariesJSON: boundaries,
		ReferenceCurvesJSON: references, ToleranceProfileJSON: toleranceJSON,
	}
	return NewSnapshot(series, recipe)
}

const missingSentinel = -9999.0

func isMissingSentinel(value float64) bool { return value == missingSentinel }

func cleanReferenceValue(channel string, hour int, value float64) float64 {
	if isMissingSentinel(value) {
		switch channel {
		case "ph":
			return 7.0
		case "temperature":
			return 30.0 + float64(hour)*0.1
		default:
			return 50.0
		}
	}
	return value
}

func fullChannels(channelNames ...string) map[string][]float64 {
	channels := map[string][]float64{}
	for _, name := range channelNames {
		series := make([]float64, 11)
		for hour := range series {
			switch name {
			case "ph":
				series[hour] = 7.0 - float64(hour)*0.02
			case "temperature":
				series[hour] = 30.0 + float64(hour)*0.1
			default:
				series[hour] = 50.0 + float64(hour)
			}
		}
		channels[name] = series
	}
	return channels
}

func TestChannelIsolationAndReweightedRecompute(t *testing.T) {
	channels := fullChannels("ph", "temperature", "do")
	// 3 of 11 observations missing for "do" => 27.3%, above the 20% threshold.
	for _, hour := range []int{1, 4, 7} {
		channels["do"][hour] = missingSentinel
	}
	snapshot := isolationFixture(t, channels)
	first, err := NewEvaluator().Evaluate(snapshot)
	if err != nil {
		t.Fatalf("evaluate with isolated channel: %v", err)
	}
	second, err := NewEvaluator().Evaluate(snapshot)
	if err != nil {
		t.Fatalf("replay evaluate: %v", err)
	}
	if first.IsolationReportJSON != second.IsolationReportJSON {
		t.Fatalf("isolation report is not replay-stable:\nfirst=%s\nsecond=%s", first.IsolationReportJSON, second.IsolationReportJSON)
	}
	var report IsolationReport
	if err := json.Unmarshal([]byte(first.IsolationReportJSON), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.IsolatedChannels) != 1 || report.IsolatedChannels[0].Channel != "do" {
		t.Fatalf("isolated channels=%+v, want [do]", report.IsolatedChannels)
	}
	if report.EffectiveChannelCount != 2 {
		t.Fatalf("effective channels=%d, want 2", report.EffectiveChannelCount)
	}
	isolated := report.IsolatedChannels[0]
	if !(isolated.MissingRate > ChannelIsolationThreshold) {
		t.Fatalf("missing rate=%v, want > %v", isolated.MissingRate, ChannelIsolationThreshold)
	}
	if isolated.WeightAfter != 0 || isolated.WeightReduction != 1 {
		t.Fatalf("isolated channel weight_after=%v reduction=%v, want 0 and 1", isolated.WeightAfter, isolated.WeightReduction)
	}
	if len(isolated.AffectedPhases) == 0 {
		t.Fatal("isolated channel must record affected phases")
	}
	if len(report.AffectedPhases) == 0 {
		t.Fatal("report must list affected phases")
	}
	for _, change := range report.PhaseWeightChanges {
		if change.WeightBefore < change.WeightAfter {
			t.Fatalf("phase %s weight increased after isolation: %v -> %v", change.Phase, change.WeightBefore, change.WeightAfter)
		}
	}
	if report.OverallScoreBefore == 0 && report.OverallScoreAfter == 0 {
		t.Fatal("overall scores before and after isolation were not retained")
	}
	var phaseScores []PhaseEvidence
	if err := json.Unmarshal([]byte(first.PhaseScoresJSON), &phaseScores); err != nil {
		t.Fatal(err)
	}
	for _, phase := range phaseScores {
		if _, present := phase.ChannelScores["do"]; present {
			t.Fatalf("phase %s still scores isolated channel do", phase.Phase)
		}
	}
}

func TestIsolationRejectedWhenFewerThanTwoEffectiveChannels(t *testing.T) {
	channels := fullChannels("ph", "temperature")
	// 3 of 11 missing for temperature => 27.3%; isolating it leaves only ph.
	for _, hour := range []int{0, 3, 9} {
		channels["temperature"][hour] = missingSentinel
	}
	_, err := NewEvaluator().Evaluate(isolationFixture(t, channels))
	if err == nil {
		t.Fatal("analysis with fewer than two effective channels must be rejected")
	}
}

func TestChannelAtTwentyPercentMissingIsNotIsolated(t *testing.T) {
	channels := fullChannels("ph", "temperature", "do", "agitation")
	// 2 of 11 = 18.2% missing, strictly below the 20% threshold (threshold is exclusive).
	for _, hour := range []int{2, 6} {
		channels["agitation"][hour] = missingSentinel
	}
	result, err := NewEvaluator().Evaluate(isolationFixture(t, channels))
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	var report IsolationReport
	if err := json.Unmarshal([]byte(result.IsolationReportJSON), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.IsolatedChannels) != 0 || report.EffectiveChannelCount != 4 {
		t.Fatalf("channels below threshold must not be isolated: %+v", report.IsolatedChannels)
	}
}

func TestIsolationRejectedWhenAPhaseLosesAllUsablePoints(t *testing.T) {
	channels := fullChannels("ph", "temperature", "do")
	// do is globally above 20%; additionally ph and temperature have no usable
	// observations in the harvest phase (hours 8,9,10), so recompute must fail.
	for _, hour := range []int{1, 4, 7} {
		channels["do"][hour] = missingSentinel
	}
	for hour := 8; hour <= 10; hour++ {
		channels["ph"][hour] = missingSentinel
		channels["temperature"][hour] = missingSentinel
	}
	if _, err := NewEvaluator().Evaluate(isolationFixture(t, channels)); err == nil {
		t.Fatal("recompute must be rejected when a phase has no usable points after isolation")
	}
}

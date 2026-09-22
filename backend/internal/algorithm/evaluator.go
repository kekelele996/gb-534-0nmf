package algorithm
import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"fermentation-kinetics-deviation-analysis/backend/internal/constants"
	"fermentation-kinetics-deviation-analysis/backend/internal/model"
	"fermentation-kinetics-deviation-analysis/backend/internal/timeseries"
	"fermentation-kinetics-deviation-analysis/backend/internal/util"
)
const Version = "phase-dtw-v1.1.0"
const (
	// ChannelIsolationThreshold is the global missing rate above which a channel is
	// quarantined for the whole analysis rather than being allowed to dominate it.
	ChannelIsolationThreshold = 0.20
	// MinimumEffectiveChannels is the smallest number of non-isolated channels
	// that must remain for an analysis to be accepted.
	MinimumEffectiveChannels = 2
)
type PhaseBoundary struct {
	Phase     constants.FermentationPhase `json:"phase"`
	StartHour float64                     `json:"start_hour"`
	EndHour   float64                     `json:"end_hour"`
}
type CurvePoint struct {
	ElapsedHour float64 `json:"elapsed_h"`
	Value       float64 `json:"value"`
}
type ChannelTolerance struct {
	Weight      float64 `json:"weight"`
	MaxDistance float64 `json:"max_distance"`
}
type Snapshot struct {
	SeriesID             uint      `json:"series_id"`
	VesselID             uint      `json:"vessel_id"`
	RecipeID             uint      `json:"recipe_id"`
	RecipeVersion        int       `json:"recipe_version"`
	RunCode              string    `json:"run_code"`
	Channel              string    `json:"channel"`
	SampleIntervalS      int       `json:"sample_interval_s"`
	PointsJSON           string    `json:"points_json"`
	SourceChecksum       string    `json:"source_checksum"`
	StartedAt            time.Time `json:"started_at"`
	EndedAt              time.Time `json:"ended_at"`
	PhaseBoundariesJSON  string    `json:"phase_boundaries_json"`
	ReferenceCurvesJSON  string    `json:"reference_curves_json"`
	ToleranceProfileJSON string    `json:"tolerance_profile_json"`
	AlgorithmVersion     string    `json:"algorithm_version"`
}
type PhaseEvidence struct {
	Phase             string             `json:"phase"`
	DurationDeviation float64            `json:"duration_deviation"`
	SlopeDeviation    float64            `json:"slope_deviation"`
	PeakTimeDeviation float64            `json:"peak_time_deviation"`
	CurveDistance     float64            `json:"curve_distance"`
	WeightedDeviation float64            `json:"weighted_deviation"`
	ChannelScores     map[string]float64 `json:"channel_scores"`
	ObservedPoints    int                `json:"observed_points"`
}
type IsolatedChannel struct {
	Channel           string  `json:"channel"`
	MissingRate       float64 `json:"missing_rate"`
	WeightBefore      float64 `json:"weight_before"`
	WeightAfter       float64 `json:"weight_after"`
	WeightReduction   float64 `json:"weight_reduction"`
	AffectedPhases    []string `json:"affected_phases"`
}
type PhaseWeightChange struct {
	Phase             string   `json:"phase"`
	WeightBefore      float64  `json:"weight_before"`
	WeightAfter       float64  `json:"weight_after"`
	WeightReduction   float64  `json:"weight_reduction"`
	ScoreBefore       float64  `json:"score_before"`
	ScoreAfter        float64  `json:"score_after"`
	IsolatedChannels  []string `json:"isolated_channels"`
}
type IsolationReport struct {
	Threshold            float64              `json:"threshold"`
	IsolatedChannels     []IsolatedChannel    `json:"isolated_channels"`
	EffectiveChannelCount int                 `json:"effective_channel_count"`
	AffectedPhases       []string             `json:"affected_phases"`
	PhaseWeightChanges   []PhaseWeightChange  `json:"phase_weight_changes"`
	OverallScoreBefore   float64              `json:"overall_score_before"`
	OverallScoreAfter    float64              `json:"overall_score_after"`
}
type phaseRun struct {
	evidence       []PhaseEvidence
	aligned        []AlignedPoint
	causes         map[string]string
	phaseWeights   map[string]float64
	channelWeights map[string]map[string]float64
	overall        float64
}
type AlignedPoint struct {
	Phase                string  `json:"phase"`
	Channel              string  `json:"channel"`
	ActualElapsedHour    float64 `json:"actual_elapsed_h"`
	ActualValue          float64 `json:"actual_value"`
	ReferenceElapsedHour float64 `json:"reference_elapsed_h"`
	ReferenceValue       float64 `json:"reference_value"`
}
type Result struct {
	PhaseScoresJSON     string
	DeviationLevel      constants.DeviationLevel
	AlignedCurveJSON    string
	SuspectedCausesJSON string
	IsolationReportJSON string
	Explanation         string
	OverallScore        float64
}
type Evaluator struct{}
func NewEvaluator() *Evaluator { return &Evaluator{} }
func NewSnapshot(series model.SensorSeries, recipe model.CultureRecipe) Snapshot {
	return Snapshot{
		SeriesID: series.ID, VesselID: series.VesselID, RecipeID: recipe.ID, RecipeVersion: recipe.Version,
		RunCode: series.RunCode, Channel: series.Channel, SampleIntervalS: series.SampleIntervalS,
		PointsJSON: series.PointsJSON, SourceChecksum: series.SourceChecksum,
		StartedAt: series.StartedAt.UTC(), EndedAt: series.EndedAt.UTC(),
		PhaseBoundariesJSON: recipe.PhaseBoundariesJSON, ReferenceCurvesJSON: recipe.ReferenceCurvesJSON,
		ToleranceProfileJSON: recipe.ToleranceProfileJSON, AlgorithmVersion: Version,
	}
}
func (s Snapshot) Canonical() (string, error) { return util.CanonicalJSON(s) }
func (s Snapshot) Hash() (string, error) {
	canonical, err := s.Canonical()
	if err != nil {
		return "", err
	}
	return util.HashString(canonical), nil
}
func DecodeSnapshot(raw string) (Snapshot, error) {
	var snapshot Snapshot
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("decode analysis snapshot: %w", err)
	}
	if snapshot.AlgorithmVersion != Version {
		return Snapshot{}, fmt.Errorf("snapshot algorithm %s is not supported by %s", snapshot.AlgorithmVersion, Version)
	}
	return snapshot, nil
}
func ValidateRecipeConfiguration(boundariesRaw, curvesRaw, toleranceRaw []byte, targetDuration float64) error {
	boundaries, curves, tolerances, err := parseConfiguration(boundariesRaw, curvesRaw, toleranceRaw)
	if err != nil {
		return err
	}
	if targetDuration <= 0 {
		return fmt.Errorf("target duration must be positive")
	}
	expected := constants.FermentationPhaseValues()
	if len(boundaries) != len(expected) {
		return fmt.Errorf("phase boundaries must contain exactly lag, growth, production, and harvest")
	}
	for i, boundary := range boundaries {
		if string(boundary.Phase) != expected[i] {
			return fmt.Errorf("phase %d must be %s", i+1, expected[i])
		}
		if boundary.StartHour < 0 || boundary.EndHour <= boundary.StartHour {
			return fmt.Errorf("phase %s has an invalid time range", boundary.Phase)
		}
		if i > 0 && math.Abs(boundary.StartHour-boundaries[i-1].EndHour) > 1e-6 {
			return fmt.Errorf("phase %s must start when the previous phase ends", boundary.Phase)
		}
	}
	if math.Abs(boundaries[len(boundaries)-1].EndHour-targetDuration) > 1e-6 {
		return fmt.Errorf("final phase must end at target_duration_h")
	}
	if len(curves) == 0 {
		return fmt.Errorf("reference curves must contain at least one channel")
	}
	for channel, points := range curves {
		if strings.TrimSpace(channel) == "" || len(points) < 4 {
			return fmt.Errorf("reference channel %s must contain at least four points", channel)
		}
		for i, point := range points {
			if i > 0 && point.ElapsedHour <= points[i-1].ElapsedHour {
				return fmt.Errorf("reference channel %s elapsed_h values must increase", channel)
			}
			if point.ElapsedHour < 0 || point.ElapsedHour > targetDuration {
				return fmt.Errorf("reference channel %s has a point outside the target duration", channel)
			}
		}
		if tolerance, ok := tolerances[channel]; ok {
			if tolerance.Weight <= 0 || tolerance.MaxDistance <= 0 {
				return fmt.Errorf("tolerance for channel %s must have positive weight and max_distance", channel)
			}
		}
	}
	return nil
}
func (e *Evaluator) Evaluate(snapshot Snapshot) (Result, error) {
	if snapshot.AlgorithmVersion != Version {
		return Result{}, fmt.Errorf("unsupported algorithm version %q", snapshot.AlgorithmVersion)
	}
	points, err := timeseries.DecodePoints(snapshot.PointsJSON)
	if err != nil {
		return Result{}, err
	}
	boundaries, references, tolerances, err := parseConfiguration(
		[]byte(snapshot.PhaseBoundariesJSON), []byte(snapshot.ReferenceCurvesJSON), []byte(snapshot.ToleranceProfileJSON),
	)
	if err != nil {
		return Result{}, err
	}
	channelOrder := sortedReferenceChannels(references)
	// First pass: every configured reference channel participates. A missing channel
	// makes the whole analysis unusable rather than being silently skipped.
	before, err := runPhases(points, snapshot.StartedAt, boundaries, references, tolerances, channelOrder, nil)
	if err != nil {
		return Result{}, err
	}
	missingRates := channelMissingRates(points, channelOrder)
	isolated := make(map[string]struct{})
	for _, channel := range channelOrder {
		if missingRates[channel] > ChannelIsolationThreshold {
			isolated[channel] = struct{}{}
		}
	}
	report := IsolationReport{
		Threshold: ChannelIsolationThreshold, IsolatedChannels: []IsolatedChannel{},
		AffectedPhases: []string{}, PhaseWeightChanges: []PhaseWeightChange{},
		OverallScoreBefore: round6(before.overall), EffectiveChannelCount: len(channelOrder) - len(isolated),
	}
	if len(channelOrder)-len(isolated) < MinimumEffectiveChannels {
		names := make([]string, 0, len(isolated))
		for channel := range isolated {
			names = append(names, channel)
		}
		sort.Strings(names)
		return Result{}, fmt.Errorf(
			"channel isolation leaves %d effective channels (minimum %d); isolated channels: %s",
			len(channelOrder)-len(isolated), MinimumEffectiveChannels, strings.Join(names, ", "),
		)
	}
	// Second pass: quarantined channels are dropped and phase weights are recomputed
	// from the remaining effective channels. Every phase must still carry evidence.
	after := before
	if len(isolated) > 0 {
		after, err = runPhases(points, snapshot.StartedAt, boundaries, references, tolerances, channelOrder, isolated)
		if err != nil {
			return Result{}, err
		}
		report.PhaseWeightChanges, report.AffectedPhases = buildPhaseWeightChanges(boundaries, before, after, isolated)
		report.IsolatedChannels = buildIsolatedChannels(channelOrder, isolated, missingRates, before, after)
	} else {
		changes, _ := buildPhaseWeightChanges(boundaries, before, before, nil)
		report.PhaseWeightChanges = changes
	}
	report.OverallScoreAfter = round6(after.overall)
	overall := after.overall
	level := constants.DeviationLevelForScore(overall)
	causeList := make([]string, 0, len(after.causes))
	for _, cause := range after.causes {
		causeList = append(causeList, cause)
	}
	sort.Strings(causeList)
	phaseJSON, err := json.Marshal(after.evidence)
	if err != nil {
		return Result{}, fmt.Errorf("encode phase evidence: %w", err)
	}
	alignedJSON, err := json.Marshal(after.aligned)
	if err != nil {
		return Result{}, fmt.Errorf("encode aligned curve: %w", err)
	}
	causesJSON, err := json.Marshal(causeList)
	if err != nil {
		return Result{}, fmt.Errorf("encode suspected causes: %w", err)
	}
	isolationJSON, err := json.Marshal(report)
	if err != nil {
		return Result{}, fmt.Errorf("encode channel isolation report: %w", err)
	}
	explanation := buildExplanation(overall, level, len(after.evidence), report)
	return Result{
		PhaseScoresJSON: string(phaseJSON), DeviationLevel: level, AlignedCurveJSON: string(alignedJSON),
		SuspectedCausesJSON: string(causesJSON), IsolationReportJSON: string(isolationJSON),
		Explanation: explanation, OverallScore: overall,
	}, nil
}
func runPhases(
	points []timeseries.Point,
	startedAt time.Time,
	boundaries []PhaseBoundary,
	references map[string][]CurvePoint,
	tolerances map[string]ChannelTolerance,
	channelOrder []string,
	excluded map[string]struct{},
) (phaseRun, error) {
	run := phaseRun{
		evidence: make([]PhaseEvidence, 0, len(boundaries)), aligned: []AlignedPoint{},
		causes:       map[string]string{},
		phaseWeights: map[string]float64{}, channelWeights: map[string]map[string]float64{},
	}
	overallWeighted, overallWeight := 0.0, 0.0
	for _, boundary := range boundaries {
		phaseEvidence, phaseAligned, phaseCauses, weight, channelWeights, phaseErr := evaluatePhase(
			points, startedAt, boundary, references, tolerances, channelOrder, excluded,
		)
		if phaseErr != nil {
			return phaseRun{}, phaseErr
		}
		phase := string(boundary.Phase)
		run.evidence = append(run.evidence, phaseEvidence)
		run.aligned = append(run.aligned, phaseAligned...)
		for key, cause := range phaseCauses {
			run.causes[key] = cause
		}
		run.phaseWeights[phase] = weight
		run.channelWeights[phase] = channelWeights
		overallWeighted += phaseEvidence.WeightedDeviation * weight
		overallWeight += weight
	}
	if overallWeight == 0 {
		return phaseRun{}, fmt.Errorf("no comparable channel observations were found")
	}
	run.overall = clamp(overallWeighted / overallWeight)
	return run, nil
}
func sortedReferenceChannels(references map[string][]CurvePoint) []string {
	channels := make([]string, 0, len(references))
	for channel := range references {
		channels = append(channels, channel)
	}
	sort.Strings(channels)
	return channels
}
func channelMissingRates(points []timeseries.Point, channels []string) map[string]float64 {
	rates := make(map[string]float64, len(channels))
	if len(points) == 0 {
		return rates
	}
	for _, channel := range channels {
		missing := 0
		for _, point := range points {
			value, ok := point.Values[channel]
			if !ok || value == nil {
				missing++
			}
		}
		rates[channel] = float64(missing) / float64(len(points))
	}
	return rates
}
func buildIsolatedChannels(
	channelOrder []string,
	isolated map[string]struct{},
	missingRates map[string]float64,
	before, after phaseRun,
) []IsolatedChannel {
	result := make([]IsolatedChannel, 0, len(isolated))
	for _, channel := range channelOrder {
		if _, ok := isolated[channel]; !ok {
			continue
		}
		entry := IsolatedChannel{
			Channel: channel, MissingRate: round6(missingRates[channel]),
			WeightBefore: round6(totalChannelWeight(before, channel)),
			WeightAfter:  round6(totalChannelWeight(after, channel)),
			AffectedPhases: []string{},
		}
		entry.WeightReduction = round6(weightReductionRatio(entry.WeightBefore, entry.WeightAfter))
		for _, evidence := range before.evidence {
			if _, scored := evidence.ChannelScores[channel]; scored {
				entry.AffectedPhases = append(entry.AffectedPhases, evidence.Phase)
			}
		}
		result = append(result, entry)
	}
	return result
}
func buildPhaseWeightChanges(
	boundaries []PhaseBoundary,
	before, after phaseRun,
	isolated map[string]struct{},
) ([]PhaseWeightChange, []string) {
	changes := make([]PhaseWeightChange, 0, len(boundaries))
	affected := []string{}
	for _, boundary := range boundaries {
		phase := string(boundary.Phase)
		weightBefore := before.phaseWeights[phase]
		weightAfter := after.phaseWeights[phase]
		change := PhaseWeightChange{
			Phase: phase, WeightBefore: round6(weightBefore), WeightAfter: round6(weightAfter),
			WeightReduction: round6(weightReductionRatio(weightBefore, weightAfter)),
			ScoreBefore: round6(phaseScore(before, phase)), ScoreAfter: round6(phaseScore(after, phase)),
			IsolatedChannels: []string{},
		}
		for channel := range isolated {
			if _, scored := evidenceForPhase(before, phase).ChannelScores[channel]; scored {
				change.IsolatedChannels = append(change.IsolatedChannels, channel)
			}
		}
		sort.Strings(change.IsolatedChannels)
		if len(change.IsolatedChannels) > 0 {
			affected = append(affected, phase)
		}
		changes = append(changes, change)
	}
	return changes, affected
}
func phaseScore(run phaseRun, phase string) float64 {
	evidence := evidenceForPhase(run, phase)
	return evidence.WeightedDeviation
}
func evidenceForPhase(run phaseRun, phase string) PhaseEvidence {
	for _, evidence := range run.evidence {
		if evidence.Phase == phase {
			return evidence
		}
	}
	return PhaseEvidence{Phase: phase, ChannelScores: map[string]float64{}}
}
func totalChannelWeight(run phaseRun, channel string) float64 {
	total := 0.0
	for phase, weights := range run.channelWeights {
		if _, scored := evidenceForPhase(run, phase).ChannelScores[channel]; scored {
			total += weights[channel]
		}
	}
	return total
}
func weightReductionRatio(before, after float64) float64 {
	if before <= 0 {
		return 0
	}
	return clamp((before - after) / before)
}
func buildExplanation(overall float64, level constants.DeviationLevel, phaseCount int, report IsolationReport) string {
	if len(report.IsolatedChannels) == 0 {
		return fmt.Sprintf(
			"Deterministic phase-constrained DTW produced an overall deviation of %.3f (%s) across %d phases. "+
				"Long gaps and missing values remain explicit; this result supports offline review only and contains no equipment control instructions.",
			overall, level, phaseCount,
		)
	}
	names := make([]string, 0, len(report.IsolatedChannels))
	for _, channel := range report.IsolatedChannels {
		names = append(names, fmt.Sprintf("%s (%.1f%% missing, weight -%.1f%%)",
			channel.Channel, channel.MissingRate*100, channel.WeightReduction*100))
	}
	return fmt.Sprintf(
		"Deterministic phase-constrained DTW quarantined %d channel(s) with missing rates above %.0f%% (%s); "+
			"phase weights were recomputed from the %d remaining effective channels for an overall deviation of %.3f (%s) across %d phases. "+
			"Pre- and post-isolation scores and weights are retained. This result supports offline review only and contains no equipment control instructions.",
		len(report.IsolatedChannels), ChannelIsolationThreshold*100, strings.Join(names, "; "),
		report.EffectiveChannelCount, overall, level, phaseCount,
	)
}
func parseConfiguration(boundariesRaw, curvesRaw, toleranceRaw []byte) (
	[]PhaseBoundary, map[string][]CurvePoint, map[string]ChannelTolerance, error,
) {
	var boundaries []PhaseBoundary
	if err := json.Unmarshal(boundariesRaw, &boundaries); err != nil {
		return nil, nil, nil, fmt.Errorf("decode phase_boundaries_json: %w", err)
	}
	var curves map[string][]CurvePoint
	if err := json.Unmarshal(curvesRaw, &curves); err != nil {
		return nil, nil, nil, fmt.Errorf("decode reference_curves_json: %w", err)
	}
	var tolerances map[string]ChannelTolerance
	if err := json.Unmarshal(toleranceRaw, &tolerances); err != nil {
		return nil, nil, nil, fmt.Errorf("decode tolerance_profile_json: %w", err)
	}
	if tolerances == nil {
		tolerances = map[string]ChannelTolerance{}
	}
	return boundaries, curves, tolerances, nil
}
func evaluatePhase(
	points []timeseries.Point,
	startedAt time.Time,
	boundary PhaseBoundary,
	references map[string][]CurvePoint,
	tolerances map[string]ChannelTolerance,
	channelOrder []string,
	excluded map[string]struct{},
) (PhaseEvidence, []AlignedPoint, map[string]string, float64, map[string]float64, error) {
	evidence := PhaseEvidence{Phase: string(boundary.Phase), ChannelScores: map[string]float64{}}
	aligned := []AlignedPoint{}
	causes := map[string]string{}
	total, totalWeight := 0.0, 0.0
	channelWeights := map[string]float64{}
	durationScores, slopeScores, peakScores, distanceScores := []float64{}, []float64{}, []float64{}, []float64{}
	for _, channel := range channelOrder {
		if _, skip := excluded[channel]; skip {
			continue
		}
		referenceAll := references[channel]
		actualTimes, actualValues := actualInPhase(points, startedAt, boundary, channel)
		referenceTimes, referenceValues := referenceInPhase(referenceAll, boundary)
		if len(actualValues) < 2 || len(referenceValues) < 2 {
			continue
		}
		median, scale := robustReferenceScale(referenceValues)
		actualScaled := scaleValues(actualValues, median, scale)
		referenceScaled := scaleValues(referenceValues, median, scale)
		distance, path, err := DTW(actualScaled, referenceScaled, maxInt(len(actualScaled), len(referenceScaled))/2+1)
		if err != nil {
			return PhaseEvidence{}, nil, nil, 0, nil, fmt.Errorf("align phase %s channel %s: %w", boundary.Phase, channel, err)
		}
		tolerance := tolerances[channel]
		if tolerance.Weight <= 0 {
			tolerance.Weight = 1
		}
		if tolerance.MaxDistance <= 0 {
			tolerance.MaxDistance = 1
		}
		curveScore := clamp(distance / tolerance.MaxDistance)
		slopeScore := clamp(math.Abs(slope(actualTimes, actualValues)-slope(referenceTimes, referenceValues)) /
			(math.Abs(slope(referenceTimes, referenceValues)) + 0.1))
		peakScore := clamp(math.Abs(peakTime(actualTimes, actualValues)-peakTime(referenceTimes, referenceValues)) /
			math.Max(boundary.EndHour-boundary.StartHour, 0.001))
		actualDuration := actualTimes[len(actualTimes)-1] - actualTimes[0]
		referenceDuration := referenceTimes[len(referenceTimes)-1] - referenceTimes[0]
		durationScore := clamp(math.Abs(actualDuration-referenceDuration) / math.Max(referenceDuration, 0.001))
		channelScore := clamp(0.50*curveScore + 0.20*slopeScore + 0.15*peakScore + 0.15*durationScore)
		evidence.ChannelScores[channel] = round6(channelScore)
		total += channelScore * tolerance.Weight
		totalWeight += tolerance.Weight
		channelWeights[channel] = tolerance.Weight
		durationScores = append(durationScores, durationScore)
		slopeScores = append(slopeScores, slopeScore)
		peakScores = append(peakScores, peakScore)
		distanceScores = append(distanceScores, curveScore)
		evidence.ObservedPoints += len(actualValues)
		for _, pair := range path {
			aligned = append(aligned, AlignedPoint{
				Phase: string(boundary.Phase), Channel: channel,
				ActualElapsedHour: round6(actualTimes[pair.ActualIndex]), ActualValue: round6(actualValues[pair.ActualIndex]),
				ReferenceElapsedHour: round6(referenceTimes[pair.ReferenceIndex]), ReferenceValue: round6(referenceValues[pair.ReferenceIndex]),
			})
		}
		if channelScore >= 0.40 {
			direction := mean(actualScaled) - mean(referenceScaled)
			causes[channel] = causeFor(channel, direction, boundary.Phase)
		}
	}
	if totalWeight == 0 {
		return PhaseEvidence{}, nil, nil, 0, nil, fmt.Errorf("phase %s has no usable channel observations", boundary.Phase)
	}
	evidence.DurationDeviation = round6(mean(durationScores))
	evidence.SlopeDeviation = round6(mean(slopeScores))
	evidence.PeakTimeDeviation = round6(mean(peakScores))
	evidence.CurveDistance = round6(mean(distanceScores))
	evidence.WeightedDeviation = round6(total / totalWeight)
	return evidence, aligned, causes, totalWeight, channelWeights, nil
}
func actualInPhase(points []timeseries.Point, startedAt time.Time, boundary PhaseBoundary, channel string) ([]float64, []float64) {
	times, values := []float64{}, []float64{}
	for _, point := range points {
		elapsed := point.Timestamp.Sub(startedAt).Hours()
		value := point.Values[channel]
		if elapsed >= boundary.StartHour && elapsed <= boundary.EndHour && value != nil {
			times = append(times, elapsed)
			values = append(values, *value)
		}
	}
	return times, values
}
func referenceInPhase(points []CurvePoint, boundary PhaseBoundary) ([]float64, []float64) {
	times, values := []float64{}, []float64{}
	for _, point := range points {
		if point.ElapsedHour >= boundary.StartHour && point.ElapsedHour <= boundary.EndHour {
			times = append(times, point.ElapsedHour)
			values = append(values, point.Value)
		}
	}
	return times, values
}
func robustReferenceScale(values []float64) (float64, float64) {
	copyValues := append([]float64(nil), values...)
	sort.Float64s(copyValues)
	median := interpolateQuantile(copyValues, 0.5)
	scale := interpolateQuantile(copyValues, 0.75) - interpolateQuantile(copyValues, 0.25)
	if math.Abs(scale) < 1e-9 {
		scale = 1
	}
	return median, scale
}
func interpolateQuantile(sorted []float64, probability float64) float64 {
	position := probability * float64(len(sorted)-1)
	lower, upper := int(math.Floor(position)), int(math.Ceil(position))
	if lower == upper {
		return sorted[lower]
	}
	return sorted[lower]*(float64(upper)-position) + sorted[upper]*(position-float64(lower))
}
func scaleValues(values []float64, median, scale float64) []float64 {
	result := make([]float64, len(values))
	for i, value := range values {
		result[i] = (value - median) / scale
	}
	return result
}
func slope(times, values []float64) float64 {
	if len(values) < 2 {
		return 0
	}
	delta := times[len(times)-1] - times[0]
	if math.Abs(delta) < 1e-12 {
		return 0
	}
	return (values[len(values)-1] - values[0]) / delta
}
func peakTime(times, values []float64) float64 {
	index := 0
	for i := 1; i < len(values); i++ {
		if values[i] > values[index] {
			index = i
		}
	}
	return times[index]
}
func causeFor(channel string, direction float64, phase constants.FermentationPhase) string {
	position := "above"
	if direction < 0 {
		position = "below"
	}
	switch strings.ToLower(channel) {
	case "do", "dissolved_oxygen", "oxygen":
		return fmt.Sprintf("%s dissolved oxygen trajectory is %s the recipe reference; review offline biomass and gas-transfer evidence.", phase, position)
	case "ph":
		return fmt.Sprintf("%s pH trajectory is %s the recipe reference; review offline metabolite and sampling evidence.", phase, position)
	case "temperature":
		return fmt.Sprintf("%s temperature trajectory is %s the recipe reference; review offline batch and sensor-calibration evidence.", phase, position)
	case "agitation", "rpm":
		return fmt.Sprintf("%s agitation observation is %s the recipe reference; review historical process and sensor evidence.", phase, position)
	default:
		return fmt.Sprintf("%s %s trajectory is %s the recipe reference; review offline process evidence.", phase, channel, position)
	}
}
func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	total := 0.0
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}
func clamp(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}
func round6(value float64) float64 { return math.Round(value*1e6) / 1e6 }

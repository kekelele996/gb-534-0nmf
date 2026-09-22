package algorithm

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"fermentation-kinetics-deviation-analysis/backend/internal/constants"
	"fermentation-kinetics-deviation-analysis/backend/internal/timeseries"
)

// IsolationMaxMissingRate bounds the per-channel missing rate tolerated by a
// deviation analysis. Channels above the threshold are isolated and the phase
// weights are recomputed from the remaining effective channels.
const IsolationMaxMissingRate = 0.20

// minEffectiveChannels is the smallest number of surviving channels required
// for a multi-channel weighted deviation to remain meaningful.
const minEffectiveChannels = 2

func evaluate(snapshot Snapshot) (Result, error) {
	points, boundaries, references, tolerances, err := decodeEvaluation(snapshot)
	if err != nil {
		return Result{}, err
	}
	// First pass: evaluate with every reference channel so the pre-isolation
	// phase scores and weights can be retained as evidence.
	beforeEvidence, beforeAligned, beforeCauses, beforeWeights, err := runPhases(
		points, snapshot.StartedAt, boundaries, references, tolerances, map[string]bool{},
	)
	if err != nil {
		return Result{}, err
	}
	beforeOverall, err := overallFromPhases(beforeEvidence, beforeWeights)
	if err != nil {
		return Result{}, err
	}

	channelRates := channelMissingRates(points, snapshot.StartedAt, boundaries, references)
	channelNames := make([]string, 0, len(channelRates))
	for channel := range channelRates {
		channelNames = append(channelNames, channel)
	}
	sort.Strings(channelNames)
	isolated := map[string]bool{}
	for _, channel := range channelNames {
		if channelRates[channel] > IsolationMaxMissingRate {
			isolated[channel] = true
		}
	}
	if len(references)-len(isolated) < minEffectiveChannels {
		return Result{}, fmt.Errorf(
			"deviation analysis rejected: %d effective channels remain after isolating channels above %.0f%% missing rate, at least %d are required",
			len(references)-len(isolated), IsolationMaxMissingRate*100, minEffectiveChannels,
		)
	}

	report := buildIsolationReport(
		beforeEvidence, beforeWeights, nil, nil, isolated, channelRates,
		beforeOverall, beforeOverall, tolerances,
	)
	if !report.Isolated {
		// Same inputs must produce the same result: without isolation the
		// second pass is identical to the first, so reuse its outputs.
		reportJSON, marshalErr := json.Marshal(report)
		if marshalErr != nil {
			return Result{}, fmt.Errorf("encode channel isolation report: %w", marshalErr)
		}
		return buildResult(
			beforeEvidence, beforeAligned, sortedCauseList(beforeCauses), beforeOverall, string(reportJSON),
			legacyExplanation(beforeOverall, len(beforeEvidence)),
		)
	}

	// Second pass: recompute over the surviving channels. Each phase must still
	// carry at least one usable channel after isolation.
	afterEvidence, aligned, causes, afterWeights, err := runPhases(
		points, snapshot.StartedAt, boundaries, references, tolerances, isolated,
	)
	if err != nil {
		return Result{}, fmt.Errorf("deviation analysis rejected after channel isolation: %w", err)
	}
	for _, evidence := range afterEvidence {
		if evidence.ObservedPoints == 0 {
			return Result{}, fmt.Errorf("deviation analysis rejected: phase %s has no usable points after channel isolation", evidence.Phase)
		}
	}
	afterOverall, err := overallFromPhases(afterEvidence, afterWeights)
	if err != nil {
		return Result{}, err
	}

	report = buildIsolationReport(
		beforeEvidence, beforeWeights, afterEvidence, afterWeights, isolated, channelRates,
		beforeOverall, afterOverall, tolerances,
	)
	reportJSON, err := json.Marshal(report)
	if err != nil {
		return Result{}, fmt.Errorf("encode channel isolation report: %w", err)
	}
	return buildResult(
		afterEvidence, aligned, sortedCauseList(causes), afterOverall, string(reportJSON),
		isolationExplanation(afterOverall, report),
	)
}

// channelMissingRates measures each reference channel over the observed
// timeline inside the recipe window: every timestamp in the deduped series is
// either present for the channel or missing. The deterministic rate uses the
// raw fraction, not a rounded display value.
func channelMissingRates(
	points []timeseries.Point,
	startedAt time.Time,
	boundaries []PhaseBoundary,
	references map[string][]CurvePoint,
) map[string]float64 {
	windowStart := boundaries[0].StartHour
	windowEnd := boundaries[len(boundaries)-1].EndHour
	rates := map[string]float64{}
	for channel := range references {
		total, missing := 0, 0
		for _, point := range points {
			elapsed := point.Timestamp.Sub(startedAt).Hours()
			if elapsed < windowStart || elapsed > windowEnd {
				continue
			}
			total++
			value, ok := point.Values[channel]
			if !ok || value == nil {
				missing++
			}
		}
		if total == 0 {
			rates[channel] = 1
			continue
		}
		rates[channel] = float64(missing) / float64(total)
	}
	return rates
}

// channelPhaseWeight returns the tolerance weight a channel contributes inside
// a phase whenever it produced a comparable score there.
func channelPhaseWeight(channel string, evidence PhaseEvidence, tolerances map[string]ChannelTolerance) float64 {
	if _, scored := evidence.ChannelScores[channel]; !scored {
		return 0
	}
	tolerance := tolerances[channel]
	if tolerance.Weight <= 0 {
		return 1
	}
	return tolerance.Weight
}

func buildIsolationReport(
	beforeEvidence []PhaseEvidence,
	beforeWeights []float64,
	afterEvidence []PhaseEvidence,
	afterWeights []float64,
	isolated map[string]bool,
	channelRates map[string]float64,
	beforeOverall, afterOverall float64,
	tolerances map[string]ChannelTolerance,
) IsolationReport {
	channelNames := make([]string, 0, len(channelRates))
	for channel := range channelRates {
		channelNames = append(channelNames, channel)
	}
	sort.Strings(channelNames)
	isolatedChannels := []IsolatedChannelReport{}
	effectiveChannels := []string{}
	for _, channel := range channelNames {
		if !isolated[channel] {
			effectiveChannels = append(effectiveChannels, channel)
			continue
		}
		weightBefore := 0.0
		for _, phase := range beforeEvidence {
			weightBefore += channelPhaseWeight(channel, phase, tolerances)
		}
		isolatedChannels = append(isolatedChannels, IsolatedChannelReport{
			Channel: channel, MissingRate: round6(channelRates[channel]),
			IsolationThreshold: IsolationMaxMissingRate,
			WeightBefore:       round6(weightBefore), WeightAfter: 0,
		})
	}
	affectedPhases := []IsolationPhaseReport{}
	for i, before := range beforeEvidence {
		phaseChannels := []string{}
		for _, channel := range channelNames {
			if isolated[channel] {
				if _, scored := before.ChannelScores[channel]; scored {
					phaseChannels = append(phaseChannels, channel)
				}
			}
		}
		if len(phaseChannels) == 0 {
			continue
		}
		reduction := 0.0
		if afterEvidence != nil && beforeWeights[i] > 0 {
			reduction = (beforeWeights[i] - afterWeights[i]) / beforeWeights[i]
		}
		afterScore, afterWeight := before.WeightedDeviation, beforeWeights[i]
		if afterEvidence != nil {
			afterScore = afterEvidence[i].WeightedDeviation
			afterWeight = afterWeights[i]
		}
		affectedPhases = append(affectedPhases, IsolationPhaseReport{
			Phase:            before.Phase,
			IsolatedChannels: phaseChannels,
			ScoreBefore:      round6(before.WeightedDeviation),
			ScoreAfter:       round6(afterScore),
			WeightBefore:     round6(beforeWeights[i]),
			WeightAfter:      round6(afterWeight),
			WeightReduction:  round6(reduction),
		})
	}
	return IsolationReport{
		Isolated: len(isolated) > 0, Threshold: IsolationMaxMissingRate,
		IsolatedChannels: isolatedChannels, AffectedPhases: affectedPhases,
		OverallBefore: round6(beforeOverall), OverallAfter: round6(afterOverall),
		EffectiveChannels: effectiveChannels,
	}
}

func isolationExplanation(after float64, report IsolationReport) string {
	if !report.Isolated {
		return legacyExplanation(after, 0)
	}
	return fmt.Sprintf(
		"Deterministic phase-constrained DTW produced an overall deviation of %.3f (%s) after isolating %d channel(s) above the %.0f%% missing-rate threshold; phase weights were recomputed across %d effective channels and the pre-isolation evidence was retained. Long gaps and missing values remain explicit; this result supports offline review only and contains no equipment control instructions.",
		after, constants.DeviationLevelForScore(after), len(report.IsolatedChannels),
		IsolationMaxMissingRate*100, len(report.EffectiveChannels),
	)
}

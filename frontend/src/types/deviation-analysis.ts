import type { DeviationLevel } from './enums/deviation-level'
import type { SensorSeries } from './sensor-series'

export type AnalysisState = 'queued' | 'analyzing' | 'completed' | 'failed' | 'reviewed' | 'confirmed' | 'investigating' | 'voided'
export interface PhaseScore {
  phase: string
  duration_deviation: number
  slope_deviation: number
  peak_time_deviation: number
  curve_distance: number
  weighted_deviation: number
  channel_scores: Record<string, number>
  observed_points: number
  weight?: number
}
export interface IsolatedChannel {
  channel: string
  missing_rate: number
  isolation_threshold: number
  weight_before: number
  weight_after: number
}
export interface IsolationPhase {
  phase: string
  isolated_channels: string[]
  score_before: number
  score_after: number
  weight_before: number
  weight_after: number
  weight_reduction: number
}
export interface IsolationReport {
  isolated: boolean
  isolation_threshold: number
  isolated_channels: IsolatedChannel[]
  affected_phases: IsolationPhase[]
  overall_score_before: number
  overall_score_after: number
  effective_channels: string[]
}
export interface AlignedPoint {
  phase: string
  channel: string
  actual_elapsed_h: number
  actual_value: number
  reference_elapsed_h: number
  reference_value: number
}
export interface DeviationAnalysis {
  id: number
  sensor_series_id: number
  recipe_id: number
  recipe_version: number
  algorithm_version: string
  input_hash: string
  phase_scores_json: PhaseScore[]
  deviation_level: DeviationLevel
  aligned_curve_json: AlignedPoint[]
  suspected_causes_json: string[]
  isolation_json?: IsolationReport | null
  analysis_state: AnalysisState
  explanation: string
  analyzed_at: string
  initiated_by: number
  initiated_by_name: string
  reviewed_by?: number
  reviewed_by_name?: string
  duration_milliseconds: number
  failure_reason?: string
  review_comment?: string
  replay_verified?: boolean
  sensor_series?: SensorSeries
  created_at: string
  updated_at: string
}

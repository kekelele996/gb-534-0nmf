<script setup lang="ts">
import { computed } from 'vue'
import { FileSearch, X } from 'lucide-vue-next'
import type { DeviationAnalysis } from '../../types/deviation-analysis'
import DeviationBadge from './DeviationBadge.vue'
import PhaseBadge from './PhaseBadge.vue'

const props = defineProps<{ modelValue: boolean; analysis: DeviationAnalysis | null }>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()
const scores = computed(() => props.analysis?.phase_scores_json ?? [])
const isolation = computed(() => props.analysis?.isolation_report_json ?? null)
const asPercent = (value: number) => `${(value * 100).toFixed(1)}%`
</script>
<template>
  <el-drawer :model-value="modelValue" size="min(680px, 94vw)" :with-header="false" @close="emit('update:modelValue', false)">
    <div class="drawer-heading">
      <div><FileSearch :size="22" /><span><small>分析解释</small><strong>#{{ analysis?.id ?? '—' }}</strong></span></div>
      <el-button text circle aria-label="关闭" @click="emit('update:modelValue', false)"><X :size="18" /></el-button>
    </div>
    <template v-if="analysis">
      <div class="explanation-lead"><DeviationBadge :level="analysis.deviation_level" /><p>{{ analysis.explanation }}</p></div>
      <section class="drawer-section">
        <h3>阶段证据</h3>
        <div v-for="score in scores" :key="score.phase" class="phase-evidence">
          <PhaseBadge :phase="score.phase" />
          <strong>{{ (score.weighted_deviation * 100).toFixed(1) }}%</strong>
          <span>曲线距离 {{ score.curve_distance.toFixed(3) }}</span>
          <span>斜率偏差 {{ score.slope_deviation.toFixed(3) }}</span>
        </div>
      </section>
      <section v-if="isolation && isolation.isolated_channels.length" class="drawer-section">
        <h3>通道隔离与降权复算</h3>
        <p class="muted">
          缺失率超过 {{ asPercent(isolation.threshold) }} 的通道被隔离，阶段权重按剩余
          {{ isolation.effective_channel_count }} 个有效通道重算。
        </p>
        <div v-for="channel in isolation.isolated_channels" :key="channel.channel" class="isolation-evidence">
          <strong>{{ channel.channel }}</strong>
          <span>缺失率 {{ asPercent(channel.missing_rate) }}</span>
          <span>降权 −{{ asPercent(channel.weight_reduction) }}</span>
          <span>阶段 {{ channel.affected_phases.join('、') || '—' }}</span>
        </div>
        <ul class="reweight-list">
          <li v-for="change in isolation.phase_weight_changes.filter((item) => item.isolated_channels.length)" :key="change.phase">
            <PhaseBadge :phase="change.phase" />
            {{ change.isolated_channels.join('、') }}：权重 {{ change.weight_before.toFixed(2) }} →
            {{ change.weight_after.toFixed(2) }}（−{{ asPercent(change.weight_reduction) }}），阶段分
            {{ asPercent(change.score_before) }} → {{ asPercent(change.score_after) }}
          </li>
        </ul>
      </section>
      <section class="drawer-section">
        <h3>疑似原因规则命中</h3>
        <p v-if="!analysis.suspected_causes_json.length" class="muted">未命中高置信度规则。</p>
        <ul v-else><li v-for="cause in analysis.suspected_causes_json" :key="cause">{{ cause }}</li></ul>
      </section>
      <dl class="evidence-grid">
        <div><dt>输入哈希</dt><dd>{{ analysis.input_hash }}</dd></div>
        <div><dt>算法版本</dt><dd>{{ analysis.algorithm_version }}</dd></div>
        <div><dt>耗时</dt><dd>{{ analysis.duration_milliseconds }} ms</dd></div>
        <div><dt>发起人</dt><dd>{{ analysis.initiated_by_name }}</dd></div>
      </dl>
    </template>
  </el-drawer>
</template>

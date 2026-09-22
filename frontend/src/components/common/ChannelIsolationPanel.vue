<script setup lang="ts">
import { computed } from 'vue'
import { ShieldAlert } from 'lucide-vue-next'
import type { IsolationReport } from '../../types/deviation-analysis'

const props = defineProps<{ report: IsolationReport | null | undefined; compact?: boolean }>()

const report = computed(() => props.report ?? null)
const percent = (value: number) => `${(value * 100).toFixed(1)}%`
const score = (value: number) => (value * 100).toFixed(1)
</script>

<template>
  <section v-if="report && report.isolated" class="isolation-panel" :class="{ compact }">
    <header class="isolation-heading">
      <ShieldAlert :size="18" />
      <div>
        <strong>通道隔离与降权复算</strong>
        <small>{{ report.isolated_channels.length }} 个通道缺失率超过 {{ percent(report.isolation_threshold) }}，已隔离并按 {{ report.effective_channels.length }} 个有效通道重算阶段权重</small>
      </div>
    </header>
    <div class="isolation-summary">
      <div><dt>隔离前总分</dt><dd>{{ score(report.overall_score_before) }}%</dd></div>
      <div><dt>复算后总分</dt><dd>{{ score(report.overall_score_after) }}%</dd></div>
      <div><dt>隔离通道</dt><dd>{{ report.isolated_channels.map((item) => item.channel).join('、') }}</dd></div>
      <div><dt>有效通道</dt><dd>{{ report.effective_channels.join('、') }}</dd></div>
    </div>
    <table class="isolation-table">
      <thead>
        <tr><th>隔离通道</th><th>缺失率</th><th>隔离前权重</th><th>隔离后权重</th></tr>
      </thead>
      <tbody>
        <tr v-for="channel in report.isolated_channels" :key="channel.channel">
          <td><code>{{ channel.channel }}</code></td>
          <td>{{ percent(channel.missing_rate) }}</td>
          <td>{{ channel.weight_before.toFixed(2) }}</td>
          <td class="isolation-zero">0.00（降权 100%）</td>
        </tr>
      </tbody>
    </table>
    <table v-if="!compact" class="isolation-table">
      <thead>
        <tr><th>受影响阶段</th><th>隔离通道</th><th>阶段分（前 → 后）</th><th>阶段权重（前 → 后）</th><th>降权比例</th></tr>
      </thead>
      <tbody>
        <tr v-for="phase in report.affected_phases" :key="phase.phase">
          <td>{{ phase.phase }}</td>
          <td>{{ phase.isolated_channels.join('、') }}</td>
          <td>{{ score(phase.score_before) }}% → <strong>{{ score(phase.score_after) }}%</strong></td>
          <td>{{ phase.weight_before.toFixed(2) }} → {{ phase.weight_after.toFixed(2) }}</td>
          <td><el-tag size="small" type="warning">{{ percent(phase.weight_reduction) }}</el-tag></td>
        </tr>
      </tbody>
    </table>
    <p v-else class="isolation-phases">受影响阶段：{{ report.affected_phases.map((phase) => phase.phase).join('、') }}</p>
  </section>
</template>

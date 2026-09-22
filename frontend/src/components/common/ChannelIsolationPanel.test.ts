import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import ChannelIsolationPanel from './ChannelIsolationPanel.vue'
import type { IsolationReport } from '../../types/deviation-analysis'

const report: IsolationReport = {
  isolated: true,
  isolation_threshold: 0.2,
  isolated_channels: [
    { channel: 'do', missing_rate: 0.333333, isolation_threshold: 0.2, weight_before: 4, weight_after: 0 },
  ],
  affected_phases: [
    { phase: 'lag', isolated_channels: ['do'], score_before: 0.18, score_after: 0.05, weight_before: 3, weight_after: 2, weight_reduction: 0.333333 },
  ],
  overall_score_before: 0.42,
  overall_score_after: 0.31,
  effective_channels: ['ph', 'temperature'],
}

describe('ChannelIsolationPanel', () => {
  it('renders isolated channels, affected phases and downgrade ratios', () => {
    const wrapper = mount(ChannelIsolationPanel, {
      props: { report },
      global: { stubs: { 'el-tag': { template: '<span><slot /></span>' } } },
    })
    const text = wrapper.text()
    expect(text).toContain('通道隔离与降权复算')
    expect(text).toContain('do')
    expect(text).toContain('33.3%')
    expect(text).toContain('lag')
    expect(text).toContain('18.0% → 5.0%')
    expect(text).toContain('3.00 → 2.00')
    expect(text).toContain('ph、temperature')
  })

  it('renders nothing when no channel was isolated', () => {
    const wrapper = mount(ChannelIsolationPanel, {
      props: { report: { ...report, isolated: false, isolated_channels: [], affected_phases: [] } },
    })
    expect(wrapper.find('.isolation-panel').exists()).toBe(false)
  })

  it('uses the compact summary inside explanation drawers', () => {
    const wrapper = mount(ChannelIsolationPanel, { props: { report, compact: true } })
    expect(wrapper.findAll('.isolation-table')).toHaveLength(1)
    expect(wrapper.find('.isolation-phases').text()).toContain('lag')
  })
})

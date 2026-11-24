import { describe, expect, it } from 'vitest'
import { buildSettingsPatch } from './buildSettingsPatch'

type Case = {
  name: string
  stored: Record<string, unknown> | undefined
  local: Record<string, unknown>
  secrets?: Record<string, string>
  cleared?: Record<string, boolean>
  want: Record<string, unknown>
}

describe('buildSettingsPatch', () => {
  const cases: Case[] = [
    {
      name: 'an unchanged key is not sent',
      stored: { site_name: 'Help', retention: 30 },
      local: { site_name: 'Help', retention: 30 },
      want: {},
    },
    {
      name: 'a changed key is sent',
      stored: { site_name: 'Help', retention: 30 },
      local: { site_name: 'Help', retention: 60 },
      want: { retention: 60 },
    },
    {
      name: '*_set keys are never sent',
      stored: { oidc_client_secret_set: false },
      local: { oidc_client_secret_set: true, site_name: 'Help' },
      want: { site_name: 'Help' },
    },
    {
      name: 'a changed array is sent',
      stored: { attachment_allowed_types: ['application/pdf'] },
      local: { attachment_allowed_types: ['application/pdf', 'image/png'] },
      want: { attachment_allowed_types: ['application/pdf', 'image/png'] },
    },
    {
      name: 'an array equal to the dump under JSON.stringify is not sent',
      stored: { attachment_allowed_types: ['application/pdf'] },
      local: { attachment_allowed_types: ['application/pdf'] },
      want: {},
    },
    {
      name: 'a key absent from the dump is sent',
      stored: { site_name: 'Help' },
      local: { site_name: 'Help', new_key: 'x' },
      want: { new_key: 'x' },
    },
    {
      name: 'a typed secret is sent trimmed',
      stored: {},
      local: {},
      secrets: { virustotal_api_key: '  abc123\n' },
      want: { virustotal_api_key: 'abc123' },
    },
    {
      name: 'a blank secret is not sent',
      stored: {},
      local: {},
      secrets: { virustotal_api_key: '   ' },
      want: {},
    },
    {
      name: 'a cleared secret is sent as the empty string',
      stored: {},
      local: {},
      cleared: { virustotal_api_key: true },
      want: { virustotal_api_key: '' },
    },
    {
      name: 'a cleared secret wins over a typed one',
      stored: {},
      local: {},
      secrets: { virustotal_api_key: 'abc' },
      cleared: { virustotal_api_key: true },
      want: { virustotal_api_key: '' },
    },
    {
      name: 'a guest figure above the overall limit is sent as the overall limit',
      stored: { guest_attachment_max_mb: 5, attachment_max_mb: 10 },
      local: { guest_attachment_max_mb: 20, attachment_max_mb: 10 },
      want: { guest_attachment_max_mb: 10 },
    },
    {
      name: 'a guest figure is clamped to 25 when the overall limit is unset',
      stored: { guest_attachment_max_mb: 5 },
      local: { guest_attachment_max_mb: 40 },
      want: { guest_attachment_max_mb: 25 },
    },
    {
      name: 'a guest figure is clamped to 25 when the overall limit is out of range',
      stored: { guest_attachment_max_mb: 5, attachment_max_mb: 500 },
      local: { guest_attachment_max_mb: 40, attachment_max_mb: 500 },
      want: { guest_attachment_max_mb: 25 },
    },
    {
      name: 'an unchanged guest figure is not clamped or sent',
      stored: { guest_attachment_max_mb: 20, attachment_max_mb: 10 },
      local: { guest_attachment_max_mb: 20, attachment_max_mb: 10 },
      want: {},
    },
    {
      name: 'with no dump every local key is sent',
      stored: undefined,
      local: { site_name: 'Help' },
      want: { site_name: 'Help' },
    },
  ]

  for (const tc of cases) {
    it(tc.name, () => {
      expect(buildSettingsPatch(tc.stored, tc.local, tc.secrets ?? {}, tc.cleared ?? {})).toEqual(tc.want)
    })
  }
})

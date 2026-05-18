/**
 * API 密钥管理
 */

import type { ClientAPIKeyConfig } from '@/types/config';
import { apiClient } from './client';

const normalizeClientAPIKey = (entry: unknown): ClientAPIKeyConfig | null => {
  if (entry === undefined || entry === null) return null;
  const record =
    entry !== null && typeof entry === 'object' && !Array.isArray(entry)
      ? (entry as Record<string, unknown>)
      : null;
  const apiKey = record?.['api-key'] ?? record?.apiKey ?? record?.key ?? (typeof entry === 'string' ? entry : '');
  const trimmed = String(apiKey || '').trim();
  if (!trimmed) return null;

  const result: ClientAPIKeyConfig = { apiKey: trimmed };
  const id = record ? String(record.id ?? '').trim() : '';
  const name = record ? String(record.name ?? '').trim() : '';
  if (id) result.id = id;
  if (name) result.name = name;
  return result;
};

const serializeClientAPIKey = (entry: ClientAPIKeyConfig): Record<string, string> => {
  const payload: Record<string, string> = {};
  const id = entry.id?.trim();
  const name = entry.name?.trim();
  if (id) payload.id = id;
  if (name) payload.name = name;
  payload['api-key'] = entry.apiKey;
  return payload;
};

const serializeClientAPIKeyPatch = (patch: { name?: string; apiKey?: string }): Record<string, string> => {
  const payload: Record<string, string> = {};
  const apiKey = patch.apiKey?.trim();
  if (Object.prototype.hasOwnProperty.call(patch, 'name')) payload.name = patch.name?.trim() ?? '';
  if (apiKey) payload['api-key'] = apiKey;
  return payload;
};

export const apiKeysApi = {
  async list(): Promise<ClientAPIKeyConfig[]> {
    const data = await apiClient.get<Record<string, unknown>>('/api-keys');
    const keys = data['api-keys'] ?? data.apiKeys;
    return Array.isArray(keys)
      ? (keys.map((key) => normalizeClientAPIKey(key)).filter(Boolean) as ClientAPIKeyConfig[])
      : [];
  },

  replace: (keys: ClientAPIKeyConfig[]) =>
    apiClient.put('/api-keys', keys.map((key) => serializeClientAPIKey(key))),

  create: (value: { name?: string; apiKey: string }) =>
    apiClient.patch('/api-keys', {
      value: serializeClientAPIKey({ apiKey: value.apiKey, name: value.name })
    }),

  update: (id: string, patch: { name?: string; apiKey?: string }) =>
    apiClient.patch('/api-keys', {
      id,
      value: serializeClientAPIKeyPatch(patch)
    }),

  delete: (id: string) => apiClient.delete(`/api-keys?id=${encodeURIComponent(id)}`),

  updateByIndex: (index: number, value: string) => apiClient.patch('/api-keys', { index, value }),

  deleteByIndex: (index: number) => apiClient.delete(`/api-keys?index=${index}`)
};

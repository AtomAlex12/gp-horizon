// VLESS servers kept on the Pi (full version, nuxk-controller /ctl/v1/vless):
// links and 3x-ui subscriptions, as many as you like. The router gets the one
// chosen as a single link, so it never fetches a subscription itself. No
// secrets come back from these calls — no links, no subscription URLs.
import { req } from './api';

export interface VlessServer {
  key: string; // name@host:port
  name: string;
  address: string;
  security: string;
  network: string;
  flow?: string;
}

export interface VlessUsage {
  upload: number;
  download: number;
  total: number; // 0 = unlimited
  expire: number; // unix s, 0 = never
}

export interface VlessSource {
  id: string;
  kind: 'link' | 'subscription';
  name?: string;
  title?: string;
  usage?: VlessUsage;
  refresh_s?: number;
  fetched_at?: number;
  skipped?: number;
  error?: string;
  servers: VlessServer[];
}

export interface VlessBook {
  sources: VlessSource[];
  active?: { source: string; key: string; at: number };
}

export const vless = {
  list: () => req<VlessBook>('GET', '/ctl/v1/vless'),
  add: (kind: VlessSource['kind'], url: string, name: string) => req<VlessBook>('POST', '/ctl/v1/vless/sources', { kind, url, name }),
  remove: (id: string) => req<VlessBook>('DELETE', `/ctl/v1/vless/sources/${id}`),
  refresh: (id: string) => req<VlessBook>('POST', `/ctl/v1/vless/sources/${id}/refresh`),
  use: (source: string, server: string) => req<VlessBook>('POST', '/ctl/v1/vless/use', { source, server }),
};

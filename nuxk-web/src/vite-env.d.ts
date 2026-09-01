/// <reference types="svelte" />
/// <reference types="vite/client" />

declare const __LITE__: boolean;

interface ImportMetaEnv {
  readonly VITE_API_BASE?: string;
  readonly VITE_API_TOKEN?: string;
}
interface ImportMeta {
  readonly env: ImportMetaEnv;
}

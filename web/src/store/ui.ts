import { create } from 'zustand';

/**
 * Shell-level UI state. TanStack Query owns server state; this store is
 * limited to ephemeral UI bits and user-tuneable shell preferences
 * (theme, sidebar collapse, brand identity).
 */
type Theme = 'light' | 'dark';

interface Brand {
  /** Short label shown next to the workspace dropdown — e.g. "Demo Indonesia". */
  name: string;
  /** Sub-label under the workspace name — usually the legal entity. */
  tagline: string;
  /** One- or two-character text mark, shown when no logo is uploaded. */
  mark: string;
  /** Optional uploaded logo, stored as a data URL. Overrides the text mark. */
  logoDataUrl: string | null;
}

const DEFAULT_BRAND: Brand = {
  name: 'Demo Indonesia',
  tagline: 'PT Demo Indonesia',
  mark: 'L',
  logoDataUrl: null,
};

interface Locale {
  /** BCP-47 tag — must be one of i18next's supportedLngs. */
  language: 'id-ID' | 'en-US';
  /** Display format token consumed by the format helpers. */
  dateFormat: 'dd/MM/yyyy' | 'yyyy-MM-dd' | 'd MMM yyyy';
  /** Group / decimal separators. id = 1.234,56 / en = 1,234.56. */
  numberFormat: 'id-ID' | 'en-US';
  /** IANA tz string. */
  timezone: string;
  /** 0 = Sunday, 1 = Monday — drives date pickers. */
  firstDayOfWeek: 0 | 1;
}

const DEFAULT_LOCALE: Locale = {
  language: 'id-ID',
  dateFormat: 'dd/MM/yyyy',
  numberFormat: 'id-ID',
  timezone: 'Asia/Jakarta',
  firstDayOfWeek: 1,
};

interface UIState {
  paletteOpen: boolean;
  setPaletteOpen: (open: boolean) => void;
  togglePalette: () => void;

  // Copilot panel state. openCopilotWith() is invoked from places like the
  // nudge bar — it opens the panel AND seeds the input/auto-sends a prompt.
  // The panel component subscribes to (copilotOpen, copilotSeedPrompt) and
  // handles the actual UI.
  copilotOpen: boolean;
  copilotSeedPrompt: string | null;
  openCopilotWith: (prompt: string) => void;
  closeCopilot: () => void;
  /** Called by the panel after it consumes the seed so we don't re-send. */
  clearCopilotSeed: () => void;

  sidebarCollapsed: boolean;
  toggleSidebar: () => void;

  theme: Theme;
  setTheme: (t: Theme) => void;
  toggleTheme: () => void;

  brand: Brand;
  setBrand: (patch: Partial<Brand>) => void;
  resetBrand: () => void;

  locale: Locale;
  setLocale: (patch: Partial<Locale>) => void;
  resetLocale: () => void;
}

const STORAGE_KEY_THEME  = 'logica.theme';
const STORAGE_KEY_BRAND  = 'logica.brand';
const STORAGE_KEY_LOCALE = 'logica.locale';

function initialTheme(): Theme {
  if (typeof window === 'undefined') return 'light';
  const stored = localStorage.getItem(STORAGE_KEY_THEME) as Theme | null;
  if (stored === 'light' || stored === 'dark') return stored;
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

function applyTheme(t: Theme) {
  const root = document.documentElement;
  if (t === 'dark') root.classList.add('dark'); else root.classList.remove('dark');
  localStorage.setItem(STORAGE_KEY_THEME, t);
}

function loadBrand(): Brand {
  if (typeof window === 'undefined') return DEFAULT_BRAND;
  try {
    const raw = localStorage.getItem(STORAGE_KEY_BRAND);
    if (!raw) return DEFAULT_BRAND;
    const parsed = JSON.parse(raw) as Partial<Brand>;
    return { ...DEFAULT_BRAND, ...parsed };
  } catch {
    return DEFAULT_BRAND;
  }
}

function saveBrand(b: Brand) {
  try { localStorage.setItem(STORAGE_KEY_BRAND, JSON.stringify(b)); } catch { /* quota; ignore */ }
}

function loadLocale(): Locale {
  if (typeof window === 'undefined') return DEFAULT_LOCALE;
  try {
    const raw = localStorage.getItem(STORAGE_KEY_LOCALE);
    if (!raw) return DEFAULT_LOCALE;
    return { ...DEFAULT_LOCALE, ...(JSON.parse(raw) as Partial<Locale>) };
  } catch {
    return DEFAULT_LOCALE;
  }
}

function saveLocale(l: Locale) {
  try { localStorage.setItem(STORAGE_KEY_LOCALE, JSON.stringify(l)); } catch { /* ignore */ }
}

export const useUI = create<UIState>()((set, get) => ({
  paletteOpen: false,
  setPaletteOpen: (open) => set({ paletteOpen: open }),
  togglePalette: () => set({ paletteOpen: !get().paletteOpen }),

  copilotOpen: false,
  copilotSeedPrompt: null,
  openCopilotWith: (prompt) => set({ copilotOpen: true, copilotSeedPrompt: prompt }),
  closeCopilot: () => set({ copilotOpen: false }),
  clearCopilotSeed: () => set({ copilotSeedPrompt: null }),

  sidebarCollapsed: false,
  toggleSidebar: () => set({ sidebarCollapsed: !get().sidebarCollapsed }),

  theme: initialTheme(),
  setTheme: (t) => { applyTheme(t); set({ theme: t }); },
  toggleTheme: () => {
    const next: Theme = get().theme === 'light' ? 'dark' : 'light';
    applyTheme(next);
    set({ theme: next });
  },

  brand: loadBrand(),
  setBrand: (patch) => {
    const next = { ...get().brand, ...patch };
    saveBrand(next);
    set({ brand: next });
  },
  resetBrand: () => {
    saveBrand(DEFAULT_BRAND);
    set({ brand: DEFAULT_BRAND });
  },

  locale: loadLocale(),
  setLocale: (patch) => {
    const next = { ...get().locale, ...patch };
    saveLocale(next);
    set({ locale: next });
  },
  resetLocale: () => {
    saveLocale(DEFAULT_LOCALE);
    set({ locale: DEFAULT_LOCALE });
  },
}));

if (typeof window !== 'undefined') {
  applyTheme(initialTheme());
}

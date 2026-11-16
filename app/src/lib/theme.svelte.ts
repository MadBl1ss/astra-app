export type ThemeChoice = 'system' | 'dark' | 'light';

// The same key index.html reads before the first paint.
const KEY = 'strelka.theme';

function load(): ThemeChoice {
  try {
    const saved = localStorage.getItem(KEY);
    if (saved === 'dark' || saved === 'light') return saved;
  } catch {
    // Storage may be unavailable; the system theme is a fine default.
  }
  return 'system';
}

/**
 * The theme is the window's own setting, the daemon knows nothing of it.
 * `?theme=dark|light` overrides it for one page load without saving: that is
 * how screenshots of both themes are taken.
 */
class Theme {
  choice = $state<ThemeChoice>(load());
  #system = matchMedia('(prefers-color-scheme: light)');
  #forced: 'dark' | 'light' | null;

  constructor() {
    const forced = new URLSearchParams(location.search).get('theme');
    this.#forced = forced === 'dark' || forced === 'light' ? forced : null;
    this.#system.addEventListener('change', () => this.#apply());
    this.#apply();
  }

  set(choice: ThemeChoice): void {
    this.choice = choice;
    this.#forced = null;
    try {
      if (choice === 'system') localStorage.removeItem(KEY);
      else localStorage.setItem(KEY, choice);
    } catch {
      // Not saved; still applied for this run.
    }
    this.#apply();
  }

  #apply(): void {
    const resolved =
      this.#forced ?? (this.choice === 'system' ? (this.#system.matches ? 'light' : 'dark') : this.choice);
    document.documentElement.dataset.theme = resolved;
  }
}

export const theme = new Theme();

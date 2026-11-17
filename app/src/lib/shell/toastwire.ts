// What the main page and the toast window say to each other, as Tauri
// events. Both sides import the names from here.

import type { ToastView } from '../toasts';

export const TOASTS_EVENTS = {
  /** main → toasts: draw this. */
  show: 'toasts://show',
  /** toasts → main: the page has loaded, send what is on screen. */
  ready: 'toasts://ready',
  /** toasts → main: a button was pressed ({id, action}); "more" opens the window. */
  action: 'toasts://action',
  /** toasts → main: the pointer is over the cards (true) or left them. */
  hover: 'toasts://hover',
} as const;

export interface ShowPayload {
  view: ToastView;
  theme: 'dark' | 'light';
}

export interface ActionPayload {
  id: string;
  action: string;
}

export const EMPTY_VIEW: ToastView = { cards: [], more: 0 };

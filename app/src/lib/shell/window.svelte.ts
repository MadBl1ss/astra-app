// Whether the window is on screen. The shell says so with an event, because
// a hidden WebView keeps reporting document.visibilityState as "visible". In
// a browser tab the value never changes and the tab's own visibility rules.
export const win = $state({ shown: true });

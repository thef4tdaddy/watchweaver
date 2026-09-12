import { useSyncExternalStore } from 'react';
const event = 'watchweaver:navigate';
export function navigate(url: string) {
  window.history.pushState(null, '', url);
  window.dispatchEvent(new Event(event));
}
const subscribe = (callback: () => void) => {
  window.addEventListener('popstate', callback);
  window.addEventListener(event, callback);
  return () => { window.removeEventListener('popstate', callback); window.removeEventListener(event, callback); };
};
export function useRoute() {
  const current = useSyncExternalStore(subscribe, () => window.location.pathname + window.location.search);
  const url = new URL(current, window.location.origin);
  return { pathname: url.pathname, search: url.search };
}
export function routeView(path: string) {
  if (path === '/' || path === '/inbox') return 'inbox';
  if (path === '/letterboxd' || path === '/movies') return 'movies';
  if (path === '/serializd' || path === '/tv') return 'tv';
  if (path === '/history') return 'history';
  if (path === '/status') return 'status';
  if (/^\/settings(?:\/(trakt|jellyfin|discord|preferences))?$/.test(path)) return 'settings';
  if (/^\/(media|tasks)\/[1-9][0-9]*$/.test(path)) return 'resource';
  return 'missing';
}

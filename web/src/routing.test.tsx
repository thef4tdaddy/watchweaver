import { act, render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { navigate, routeView, useRoute } from './routing';
function Probe(){const route=useRoute();return <p>{route.pathname}{route.search}</p>;}
describe('application routes',()=>{
 it('opens exact workflow and resource destinations',()=>{
  expect(routeView('/letterboxd')).toBe('movies');
  expect(routeView('/serializd')).toBe('tv');
  expect(routeView('/settings/trakt')).toBe('settings');
  expect(routeView('/tasks/42')).toBe('resource');
  expect(routeView('/media/0')).toBe('missing');
  expect(routeView('/settings/unknown')).toBe('missing');
 });
 it('restores URL filters on navigation and browser history events',()=>{
  window.history.replaceState(null,'','/inbox?type=movie');render(<Probe/>);
  expect(screen.getByText('/inbox?type=movie')).toBeInTheDocument();
  act(()=>navigate('/history?page=2&q=Example'));
  expect(screen.getByText('/history?page=2&q=Example')).toBeInTheDocument();
  act(()=>{window.history.replaceState(null,'','/inbox?type=movie');window.dispatchEvent(new PopStateEvent('popstate'));});
  expect(screen.getByText('/inbox?type=movie')).toBeInTheDocument();
 });
});

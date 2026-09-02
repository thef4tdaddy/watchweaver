import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import App from './App'

const integrations={trakt:{authorization:{status:'connected'},poll:{phase:'polling',consecutive_failures:0}},letterboxd:{enabled:true,status:'available'},serializd:{enabled:true,status:'enabled'},discord:{enabled:false,status:'disabled'}}
const settings={timezone:'UTC',trakt_poll_minutes:5,prompt_movies_enabled:true,prompt_tv_enabled:true,serializd_enabled:true,serializd_reminder_changes:20,serializd_reminder_days:14}
const task={id:4,type:'rating_review',state:'pending',created_at:'2026-09-02T00:00:00Z',media:{id:9,type:'movie',title:'The Example',year:2026,external_ids:{trakt:'9'}}}
function json(body:unknown,status=200){return Promise.resolve(new Response(JSON.stringify(body),{status,headers:{'Content-Type':'application/json'}}))}

beforeEach(()=>{vi.stubGlobal('fetch',vi.fn((input:RequestInfo|URL,init?:RequestInit)=>{const path=String(input);if(path==='/api/integrations')return json(integrations);if(path==='/api/inbox')return json({page:1,per_page:50,total:1,total_pages:1,items:[task]});if(path==='/api/media/9/rating')return json({media_id:9,rating:8,stars:4});if(path==='/api/settings')return json(settings);if(path==='/api/serializd')return json({enabled:true,pending_changes:2,pending_episode_watches:2,pending_rating_changes:0,count_threshold_reached:false,elapsed_threshold_reached:false,due:false,unsupported_season_ratings:0,unsupported_tv_reviews:0,reminder_changes:20,reminder_days:14,import_url:'https://serializd.example/import'});if(path==='/api/letterboxd')return json({pending_rows:1,pending_events:1,duplicate_warnings:0,generated_batches:0});if(path==='/api/letterboxd/batches')return json({items:[]});if(path.startsWith('/api/tasks/')&&init?.method==='POST')return json({state:'completed'});return json({error:'not found'},404)}))})
afterEach(()=>{cleanup();vi.unstubAllGlobals()})

describe('WatchWeaver dashboard',()=>{
  it('renders the actionable inbox and existing current rating',async()=>{render(<App/>);expect(screen.getByRole('heading',{level:1,name:'Inbox'})).toBeInTheDocument();expect(await screen.findByText('The Example')).toBeInTheDocument();expect(screen.getByText(/Current rating:/)).toHaveTextContent('4 ★');expect(screen.getByText(/does not create a historical snapshot/i)).toBeInTheDocument()})
  it('navigates to Serializd status and settings without rendering secrets',async()=>{render(<App/>);await screen.findByText('The Example');fireEvent.click(screen.getByRole('button',{name:/Television/}));expect(await screen.findByText('Television is in rhythm')).toBeInTheDocument();fireEvent.click(screen.getByRole('button',{name:/Settings/}));expect(await screen.findByText('Integration availability')).toBeInTheDocument();expect(screen.queryByText(/webhook-secret|access_token|refresh_token/i)).not.toBeInTheDocument()})
  it('submits exact canonical rating values',async()=>{render(<App/>);await screen.findByText('The Example');fireEvent.click(screen.getByTitle('4.5 stars'));fireEvent.click(screen.getByRole('button',{name:'Save & complete'}));await waitFor(()=>expect(fetch).toHaveBeenCalledWith('/api/tasks/4/complete',expect.objectContaining({method:'POST',body:JSON.stringify({rating:9})})))})
})

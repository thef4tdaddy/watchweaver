import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import SetupGate from './SetupGate'

afterEach(()=>{cleanup();vi.unstubAllGlobals()})
const json=(body:unknown,status=200)=>Promise.resolve(new Response(JSON.stringify(body),{status,headers:{'Content-Type':'application/json'}}))

describe('first-run setup',()=>{
  it('stores write-only Trakt credentials and completes device authorization',async()=>{
    const fetchMock=vi.fn((input:RequestInfo|URL)=>{const path=String(input)
      if(path==='/api/setup')return json({complete:false,encrypted_storage:true,trakt:{configured:false,authorization_status:'not_configured',client_id_overridden:false,client_secret_overridden:false},discord:{configured:false,enabled:false,webhook_overridden:false}})
      if(path==='/api/integrations')return json({trakt:{authorization:{status:'not_configured'},poll:{consecutive_failures:0}},letterboxd:{enabled:true,status:'available'},serializd:{enabled:false,status:'disabled'},discord:{enabled:false,status:'disabled'}})
      if(path==='/api/inbox')return json({page:1,per_page:50,total:0,total_pages:0,items:[]})
      if(path==='/api/integrations/trakt/config')return json({configured:true})
      if(path==='/api/integrations/trakt/authorize')return json({status:'authorization_pending',user_code:'ABCD',verification_url:'https://trakt.tv/activate'},202)
      if(path==='/api/integrations/trakt/authorize/poll')return json({status:'connected'})
      return json({error:'not found'},404)
    })
    vi.stubGlobal('fetch',fetchMock);render(<SetupGate/>);expect(await screen.findByText('Set up WatchWeaver')).toBeInTheDocument()
    fireEvent.change(screen.getByPlaceholderText('Paste Trakt client ID'),{target:{value:'client-id'}})
    fireEvent.change(screen.getByPlaceholderText('Paste Trakt client secret'),{target:{value:'client-secret'}})
    fireEvent.click(screen.getByRole('button',{name:'Save and connect'}));expect(await screen.findByText('ABCD')).toBeInTheDocument()
    await waitFor(()=>expect(fetchMock).toHaveBeenCalledWith('/api/integrations/trakt/config',expect.objectContaining({body:JSON.stringify({client_id:'client-id',client_secret:'client-secret'})})))
    fireEvent.click(screen.getByRole('button',{name:'I authorized it'}));expect(await screen.findByText('✓ Trakt connected')).toBeInTheDocument()
    expect(screen.queryByDisplayValue('client-secret')).not.toBeInTheDocument()
  })

  it('shows environment-managed credentials as locked',async()=>{
    vi.stubGlobal('fetch',vi.fn((input:RequestInfo|URL)=>String(input)==='/api/setup'?json({complete:false,encrypted_storage:true,trakt:{configured:true,authorization_status:'not_authorized',client_id_overridden:true,client_secret_overridden:true},discord:{configured:false,enabled:false,webhook_overridden:false}}):json({error:'not found'},404)))
    render(<SetupGate/>);expect(await screen.findByText(/Environment-managed fields are locked/i)).toBeInTheDocument();screen.getAllByPlaceholderText('Locked by environment').forEach(field=>expect(field).toBeDisabled())
  })
})

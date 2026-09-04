import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import SetupGate from './SetupGate'

afterEach(()=>{cleanup();vi.unstubAllGlobals()})
const json=(body:unknown,status=200)=>Promise.resolve(new Response(JSON.stringify(body),{status,headers:{'Content-Type':'application/json'}}))

describe('first-run setup',()=>{
  it('shows the branded startup artwork while setup status loads',()=>{
    vi.stubGlobal('fetch',vi.fn(()=>new Promise(()=>{})))
    render(<SetupGate/>);expect(screen.getByRole('img',{name:'WatchWeaver'})).toHaveAttribute('src','/brand/watchweaver-splash.png')
  })

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
    expect(screen.getByText(/Trakt VIP is currently required for new API applications/i)).toBeInTheDocument()
    expect(screen.getByText(/Trakt policy, not a WatchWeaver subscription/i)).toBeInTheDocument()
    expect(screen.getByText('PRIVATE LAN / VPN ONLY')).toHaveClass('network-boundary')
    fireEvent.click(screen.getByText('How to get your Trakt Client ID and Secret'))
    screen.getAllByRole('link',{name:/Trakt API applications/i}).forEach(link=>expect(link).toHaveAttribute('href','https://trakt.tv/oauth/applications'))
    expect(screen.getByText('urn:ietf:wg:oauth:2.0:oob')).toBeInTheDocument()
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

  it('turns rejected Trakt credentials into actionable help',async()=>{
    vi.stubGlobal('fetch',vi.fn((input:RequestInfo|URL)=>{
      const path=String(input)
      if(path==='/api/setup')return json({complete:false,encrypted_storage:true,trakt:{configured:false,authorization_status:'not_configured',client_id_overridden:false,client_secret_overridden:false},discord:{configured:false,enabled:false,webhook_overridden:false}})
      if(path==='/api/integrations/trakt/config')return json({error:'Trakt returned HTTP 401: invalid client'},401)
      return json({error:'not found'},404)
    }))
    render(<SetupGate/>);await screen.findByText('Set up WatchWeaver')
    fireEvent.change(screen.getByPlaceholderText('Paste Trakt client ID'),{target:{value:'bad-id'}})
    fireEvent.change(screen.getByPlaceholderText('Paste Trakt client secret'),{target:{value:'bad-secret'}})
    fireEvent.click(screen.getByRole('button',{name:'Save and connect'}))
    expect(await screen.findByText(/Check the Client ID and Secret/i)).toBeInTheDocument()
  })
})

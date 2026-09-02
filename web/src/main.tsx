import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import './network-boundary.css'
import SetupGate from './SetupGate.tsx'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <SetupGate />
  </StrictMode>,
)

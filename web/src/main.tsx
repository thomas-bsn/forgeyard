import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import App from './App'
import { reloadOnStaleCode } from './staleCode'
import { applyStoredTheme } from './ui'
import './styles.css'

applyStoredTheme()
reloadOnStaleCode()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)

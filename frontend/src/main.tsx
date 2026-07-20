import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { App } from './App'
import './styles/base-shell.css'
import './styles/data-workspaces.css'
import './styles/theme-system.css'
import './styles/domains.css'
import './styles/interaction-accessibility.css'

createRoot(document.getElementById('root')!).render(<StrictMode><App /></StrictMode>)

import type { ErrorInfo } from 'react'
import { createRoot } from 'react-dom/client'

import './style.css'
import App from './App'
import { reportError } from './utils/error-reporter'

const rootElement = document.getElementById('app')

if (!rootElement) {
  throw new Error('Missing #app root element')
}

function reportReactError(error: unknown, errorInfo: ErrorInfo) {
  reportError(error instanceof Error ? error : new Error(String(error)), {
    componentStack: errorInfo.componentStack,
  })
}

createRoot(rootElement, {
  onCaughtError: reportReactError,
  onRecoverableError: reportReactError,
  onUncaughtError: reportReactError,
}).render(<App />)

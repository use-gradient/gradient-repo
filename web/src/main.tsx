import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import { ClerkProvider } from '@clerk/clerk-react'
import { dark } from '@clerk/themes'
import App from './App'
import './index.css'

const CLERK_KEY = import.meta.env.VITE_CLERK_PUBLISHABLE_KEY

if (!CLERK_KEY) {
  console.warn('[Gradient] VITE_CLERK_PUBLISHABLE_KEY not set. Auth will not work.')
}

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <BrowserRouter>
      <ClerkProvider
        publishableKey={CLERK_KEY || 'pk_test_placeholder'}
        appearance={{
          baseTheme: dark,
          variables: {
            colorPrimary: '#5a9a94',
            colorBackground: '#0d0e10',
            colorText: '#b4bcd0',
            colorInputBackground: '#08090a',
            colorInputText: '#b4bcd0',
            borderRadius: '2px',
            fontFamily: "'Space Grotesk', system-ui, sans-serif",
          },
          elements: {
            rootBox: { backgroundColor: '#08090a' },
            card: { backgroundColor: '#0d0e10', borderColor: '#1a1c20', boxShadow: 'none' },
            headerTitle: { color: '#b4bcd0' },
            headerSubtitle: { color: '#5c6578' },
            formButtonPrimary: { backgroundColor: '#5a9a94' },
            formFieldInput: { backgroundColor: '#08090a', borderColor: '#1a1c20', color: '#b4bcd0' },
            footerActionLink: { color: '#5a9a94' },
          },
        }}
      >
        <App />
      </ClerkProvider>
    </BrowserRouter>
  </React.StrictMode>,
)

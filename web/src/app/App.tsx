import { BrowserRouter } from 'react-router'
import { AppRoutes } from './routes'
import { SessionProvider } from './session'

export function App() {
  return (
    <BrowserRouter>
      <SessionProvider>
        <AppRoutes />
      </SessionProvider>
    </BrowserRouter>
  )
}

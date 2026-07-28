import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import App from './App';
import { isAuthRedirect } from './lib/auth';
import './styles.css';

const root = createRoot(document.getElementById('root')!);

// The Entra redirect URI is this app's own origin, so the sign-in popup comes
// back to "/" carrying ?code=&state=. Booting the board inside that popup
// would render the identity gate again — a second login page that never
// closes. MSAL runs in the opener, reads those parameters off the popup and
// closes it, so here we only show a placeholder and stay out of its way.
if (isAuthRedirect()) {
  root.render(<p className="auth-return">Completing sign-in…</p>);
} else {
  root.render(
    <StrictMode>
      <App />
    </StrictMode>,
  );
}

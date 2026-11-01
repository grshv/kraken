import {TooltipProvider} from './components/ui/tooltip';
import {createRoot} from 'react-dom/client';
import {ThemeProvider} from './ThemeProvider';
import {DemoProvider} from './store';
import {App} from './App';
import './theme.css';
import './style.css';
createRoot(document.getElementById('root')!).render(<ThemeProvider><DemoProvider><TooltipProvider delayDuration={400}><App/></TooltipProvider></DemoProvider></ThemeProvider>);

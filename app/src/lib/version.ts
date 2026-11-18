import { version } from '../../package.json';

/**
 * The app's version. In a browser it is package.json's; the real window
 * replaces it at start with the build's own (main.ts), which is what a test
 * build stamped with another version reports.
 */
export const app = { version };

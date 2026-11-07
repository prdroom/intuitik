// Browser texts of packet M1-T2 section 3.3, three lines each. The server's
// exchange-refusal 403 body is the B-RF lines joined with "\n" (internal/server/texts.go).
const restartInstead =
  'If your terminal shows "someone else may have connected first", restart intuitik instead of pressing Enter.';

export const NO_SESSION = [
  'No session in this tab.',
  'To open the panel, press Enter in the terminal where intuitik is running. A new login address opens in your browser. You can also restart intuitik.',
  restartInstead,
];

export const REFUSED = [
  'This login address is no longer valid (red stub). It was already used, or it expired after 60 seconds.',
  'Press Enter in the terminal where intuitik is running to open a new login address, or restart intuitik.',
  restartInstead,
];

export const STOPPED = [
  'intuitik has stopped, and this tab is disconnected.',
  'Close this tab. To continue, run intuitik again; it opens a new tab.',
];

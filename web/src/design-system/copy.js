// C-05 Copy: puts the exact text on the clipboard (leading and trailing spaces kept) and
// reports "copied" or "failed". A refused or missing clipboard is a failure, never a throw.
export function browserWriteText(text) {
  if (typeof navigator === 'undefined' || !navigator.clipboard || !navigator.clipboard.writeText) {
    return Promise.reject(new Error('clipboard unavailable'));
  }
  return navigator.clipboard.writeText(text);
}

export function createCopyHandler({ text, writeText = browserWriteText, onResult }) {
  return async function copy() {
    let result;
    try {
      await writeText(text);
      result = 'copied';
    } catch {
      result = 'failed';
    }
    if (onResult) {
      onResult(result);
    }
    return result;
  };
}

// Port coordinates (C-12, packet DS-1 T-10). A reason line wraps, so a row is as tall as
// its content and a port's centre comes from the laid-out row heights, not from a fixed row
// height. The card's rows start below the header and the row padding and are separated by
// the row gap.
import tokens from './tokens.js';

export function rowCenters(rowHeights, size = tokens.size) {
  let top = size.cardHeader + size.rowPad;
  return rowHeights.map((height) => {
    const centre = top + height / 2;
    top += height + size.rowGap;
    return centre;
  });
}

// The card's incoming port sits on the left edge at the header's height.
export function inPortCenter(size = tokens.size) {
  return size.cardHeader / 2;
}

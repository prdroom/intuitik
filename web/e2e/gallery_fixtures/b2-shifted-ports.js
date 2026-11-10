// B-2 negative fixture: the card reports each port centre one row gap (4 px) below the
// centre of its row.
const card = document.querySelector('.ds-card');
const cardTop = card.getBoundingClientRect().top;
const out = {};
for (const row of card.querySelectorAll('[data-row-id]')) {
  const r = row.getBoundingClientRect();
  out[row.dataset.rowId] = r.top - cardTop + r.height / 2 + 4;
}
const header = card.querySelector('.ds-card__header').getBoundingClientRect();
document.querySelector('[data-port-centres="NEGATIVE"]').textContent = JSON.stringify({ in: header.top - cardTop + header.height / 2, out });

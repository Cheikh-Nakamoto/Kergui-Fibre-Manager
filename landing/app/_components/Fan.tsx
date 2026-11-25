'use client';

import { Children, useEffect, useRef, useState } from 'react';
import { ChevronIcon } from './icons';

const itemsOf = (s: HTMLElement) => Array.from(s.children) as HTMLElement[];
// The scroll position that centres `el` in the strip.
const offsetFor = (s: HTMLElement, el: HTMLElement) => el.offsetLeft - (s.clientWidth - el.offsetWidth) / 2;

// The fan of phones. On a wide screen all five show, fanned out, and the
// controls are hidden; narrower, the fan becomes a swipeable strip (scroll
// snap) that opens on the middle phone, with working previous / next buttons.
export function Fan({ children }: { children: React.ReactNode }) {
  const strip = useRef<HTMLDivElement>(null);
  const count = Children.count(children);
  const middle = Math.floor(count / 2);
  const [active, setActive] = useState(middle);

  useEffect(() => {
    const s = strip.current;
    if (!s || s.scrollWidth <= s.clientWidth) return;
    const el = itemsOf(s)[middle];
    if (el) s.scrollLeft = offsetFor(s, el);
  }, [middle]);

  const onScroll = () => {
    const s = strip.current;
    if (!s) return;
    let best = 0;
    let dist = Infinity;
    itemsOf(s).forEach((el, i) => {
      const d = Math.abs(offsetFor(s, el) - s.scrollLeft);
      if (d < dist) {
        dist = d;
        best = i;
      }
    });
    setActive(best);
  };

  const go = (i: number) => {
    const s = strip.current;
    const el = s ? itemsOf(s)[i] : undefined;
    if (!s || !el) return;
    const smooth = !window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;
    s.scrollTo({ left: offsetFor(s, el), behavior: smooth ? 'smooth' : 'auto' });
  };

  return (
    <div className="fan-wrap">
      <div className="fan" data-sp="" ref={strip} onScroll={onScroll}>
        {children}
      </div>
      <div className="fan-ctrl">
        <button type="button" aria-label="Écran précédent" disabled={active === 0} onClick={() => go(active - 1)}>
          <ChevronIcon dir="left" size={18} />
        </button>
        <div className="fan-dots" aria-hidden="true">
          {Array.from({ length: count }, (_, i) => (
            <span key={i} className={i === active ? 'on' : undefined} />
          ))}
        </div>
        <button type="button" aria-label="Écran suivant" disabled={active === count - 1} onClick={() => go(active + 1)}>
          <ChevronIcon dir="right" size={18} />
        </button>
      </div>
    </div>
  );
}

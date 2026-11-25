'use client';

import { useEffect } from 'react';

// The homepage's scroll motion. Without JS (or with reduced motion) nothing
// here runs and every style it drives stays off: they only apply under
// `.motion-on`, which this adds to the [data-kroot] element.
//  - [data-stagger] staggers its children's reveal delay (--dl)
//  - .rv gets `in` once it enters the viewport, then `done` (snappy hovers)
//  - [data-px] parallax → --py; [data-sx] inside [data-sp] fans out → --sxv
//  - .grid-bg → --bgy, .steps → --prog, page progress → --pg
export function Motion() {
  useEffect(() => {
    const root = document.querySelector<HTMLElement>('[data-kroot]');
    if (!root) return;
    if (window.matchMedia?.('(prefers-reduced-motion: reduce)').matches) return;
    root.classList.add('motion-on');

    root.querySelectorAll<HTMLElement>('[data-stagger]').forEach((box) => {
      const step = parseFloat(box.dataset.stagger ?? '') || 0.08;
      Array.from(box.children).forEach((c, i) => (c as HTMLElement).style.setProperty('--dl', `${(i * step).toFixed(2)}s`));
    });

    const reveals = Array.from(root.querySelectorAll<HTMLElement>('.rv:not(.in)'));
    const pxs = Array.from(root.querySelectorAll<HTMLElement>('[data-px]')).map((el) => ({
      el,
      f: parseFloat(el.dataset.px ?? '') || 0,
      y: 0,
    }));
    const sxs = Array.from(root.querySelectorAll<HTMLElement>('[data-sx]')).map((el) => ({
      el,
      x: parseFloat(el.dataset.sx ?? '') || 0,
      box: (el.closest('[data-sp]') as HTMLElement | null) ?? el.parentElement ?? el,
    }));
    const grids = Array.from(root.querySelectorAll<HTMLElement>('.grid-bg'));
    const steps = Array.from(root.querySelectorAll<HTMLElement>('.steps'));
    const timers: number[] = [];
    const clamp = (v: number, a: number, b: number) => Math.min(b, Math.max(a, v));

    let raf = 0;
    const frame = () => {
      raf = 0;
      const vh = window.innerHeight || 800;
      const vw = window.innerWidth || 1200;
      const amt = vh > 2000 ? 0 : vw < 720 ? 0.4 : 1;
      for (let i = reveals.length - 1; i >= 0; i--) {
        const el = reveals[i];
        if (el.getBoundingClientRect().top < vh * 0.88) {
          el.classList.add('in');
          reveals.splice(i, 1);
          timers.push(window.setTimeout(() => el.classList.add('done'), 2600));
        }
      }
      for (const p of pxs) {
        const r = p.el.getBoundingClientRect();
        const center = r.top - p.y + r.height / 2;
        p.y = clamp((center - vh / 2) * -p.f * amt, -180, 180);
        p.el.style.setProperty('--py', `${p.y.toFixed(1)}px`);
      }
      for (const s of sxs) {
        const r = s.box.getBoundingClientRect();
        const e = 1 - Math.pow(1 - clamp((vh - r.top) / (vh * 0.85), 0, 1), 3);
        s.el.style.setProperty('--sxv', `${(amt ? s.x * (1 - e) : 0).toFixed(1)}px`);
      }
      for (const g of grids) g.style.setProperty('--bgy', `${(g.getBoundingClientRect().top * -0.2 * amt).toFixed(1)}px`);
      for (const s of steps) {
        const r = s.getBoundingClientRect();
        s.style.setProperty('--prog', clamp((vh * 0.65 - r.top) / Math.max(1, r.height - 100), 0, 1).toFixed(3));
      }
      const rr = root.getBoundingClientRect();
      root.style.setProperty('--pg', clamp(-rr.top / Math.max(1, rr.height - vh), 0, 1).toFixed(4));
    };
    const onScroll = () => {
      if (!raf) raf = requestAnimationFrame(frame);
    };
    document.addEventListener('scroll', onScroll, { passive: true, capture: true });
    window.addEventListener('resize', onScroll, { passive: true });
    frame();

    return () => {
      document.removeEventListener('scroll', onScroll, { capture: true });
      window.removeEventListener('resize', onScroll);
      if (raf) cancelAnimationFrame(raf);
      timers.forEach(clearTimeout);
      root.classList.remove('motion-on');
    };
  }, []);
  return null;
}

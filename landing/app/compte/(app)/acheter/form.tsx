'use client';

import { useActionState, useState } from 'react';
import { checkout, type FormState } from '../../actions';
import { reloadOnStale } from '../../../../lib/fresh';
import { termLabel, type Pass } from '../../../../lib/passes';
import { date, fcfa, tierLabel } from '../../../../lib/format';

export type BoxOption = { boxId: string; tier: string; expiresAt?: string; upgradeHours: number };

function duration(hours: number): string {
  const d = Math.floor(hours / 24);
  const h = hours % 24;
  if (d === 0) return `${h} h`;
  return h ? `${d} j ${h} h` : `${d} j`;
}

export function BuyForm({
  passes,
  boxes,
  initialBox,
  initialTier,
}: {
  passes: Pass[];
  boxes: BoxOption[];
  initialBox: string;
  initialTier: 'basic' | 'max';
}) {
  const [state, action, pending] = useActionState<FormState, FormData>(reloadOnStale(checkout), {});
  const [box, setBox] = useState(initialBox);
  const [tier, setTier] = useState<'basic' | 'max'>(initialTier);
  const [code, setCode] = useState(`${initialTier}_1m`);

  const current = boxes.find((b) => b.boxId === box);
  const maxRunning = current?.tier === 'max';
  const effectiveTier = maxRunning ? 'max' : tier;
  const list = passes.filter((p) => p.tier === effectiveTier);
  const selected = list.find((p) => p.code === code) ?? list[0];

  function pickTier(t: 'basic' | 'max') {
    setTier(t);
    setCode(`${t}_${selected?.term ?? '1m'}`);
  }

  return (
    <form action={action} className="cl-form">
      <label>
        Box
        <select name="box" value={box} onChange={(e) => setBox(e.target.value)}>
          {boxes.map((b) => (
            <option key={b.boxId} value={b.boxId}>
              {b.boxId} · {tierLabel[b.tier] ?? b.tier}
              {b.expiresAt ? ` jusqu’au ${date(b.expiresAt)}` : ''}
            </option>
          ))}
          <option value="__none">Pas encore de box (le pass attend la première connexion de l’application)</option>
          <option value="__new">Une autre box (numéro de série)</option>
        </select>
      </label>
      {box === '__new' ? (
        <label>
          Numéro de série de la box
          <input
            name="newBox"
            type="text"
            placeholder="ZTEG1A2B3C4D"
            autoCapitalize="characters"
            spellCheck={false}
            required
          />
          <span className="cl-muted" style={{ fontWeight: 400, fontSize: '0.9rem' }}>
            Sur l’étiquette sous la box (« S/N »), ou dans l’application Kergui.
          </span>
        </label>
      ) : null}

      <fieldset>
        <legend>Niveau</legend>
        <div className="cl-choices">
          {(['basic', 'max'] as const).map((t) => (
            <label key={t} className="cl-choice" aria-disabled={t === 'basic' && maxRunning}>
              <input
                type="radio"
                name="tier"
                value={t}
                checked={effectiveTier === t}
                disabled={t === 'basic' && maxRunning}
                onChange={() => pickTier(t)}
              />
              <strong>{tierLabel[t]}</strong>
              <span>
                {t === 'basic'
                  ? maxRunning
                    ? 'Max est actif sur cette box : prolongez avec Max.'
                    : 'Bloquer un appareil, couper le Wi-Fi.'
                  : 'Tout Basic, plus l’historique, le Wi-Fi et le rejet automatique.'}
              </span>
            </label>
          ))}
        </div>
      </fieldset>

      <fieldset>
        <legend>Durée</legend>
        <div className="cl-choices">
          {list.map((p) => (
            <label key={p.code} className="cl-choice">
              <input
                type="radio"
                name="pass"
                value={p.code}
                checked={selected?.code === p.code}
                onChange={() => setCode(p.code)}
              />
              <strong>{termLabel[p.term]}</strong>
              <span>
                {fcfa(p.priceXof)} · {p.days} jours
              </span>
            </label>
          ))}
        </div>
      </fieldset>

      {current?.tier === 'basic' && effectiveTier === 'max' && current.upgradeHours > 0 ? (
        <p className="cl-info">
          Passage à Max : le temps Basic qui vous reste devient {duration(current.upgradeHours)} de Max (au prorata de
          650 / 2 000 F), ajoutés aux {selected?.days} jours du pass.
        </p>
      ) : current && current.tier === effectiveTier && current.expiresAt ? (
        <p className="cl-info">
          Les {selected?.days} jours s’ajoutent à la fin du pass actuel ({date(current.expiresAt)}).
        </p>
      ) : null}

      {state.error ? (
        <p className="cl-error" role="alert">
          {state.error}
        </p>
      ) : null}
      <div>
        <button type="submit" className="cl-btn" disabled={pending || !selected}>
          {pending ? 'Ouverture du paiement…' : selected ? `Payer ${fcfa(selected.priceXof)}` : 'Payer'}
        </button>
      </div>
    </form>
  );
}

import Link from 'next/link';
import type { Metadata } from 'next';
import { redirect } from 'next/navigation';
import { basicToMaxHours, catalogue, licensing, type Subscription } from '../../../../lib/account';
import { site } from '../../../../lib/site';
import { BuyForm, type BoxOption } from './form';

export const metadata: Metadata = { title: 'Prendre un pass' };

export default async function BuyPage({ searchParams }: { searchParams: Promise<{ box?: string; tier?: string }> }) {
  if (!site.buyEnabled) redirect('/compte');
  const q = await searchParams;
  const [{ passes }, { subscriptions }] = await Promise.all([
    catalogue(),
    licensing<{ subscriptions: Subscription[] }>('subscriptions'),
  ]);

  const boxes: BoxOption[] = subscriptions
    // A lifetime pass (issue #98) has nothing to buy: the hub refuses it.
    .filter((s) => s.channel !== 'play' && !(s.active && s.lifetime))
    .map((s) => {
      const tier = s.active ? s.effectiveTier : 'free';
      return {
        boxId: s.boxId,
        tier,
        expiresAt: s.active ? s.expiresAt : undefined,
        upgradeHours: tier === 'basic' ? basicToMaxHours(s.expiresAt) : 0,
      };
    });
  const box = boxes.some((b) => b.boxId === q.box) ? q.box! : (boxes[0]?.boxId ?? '__none');
  const tier = q.tier === 'basic' ? 'basic' : 'max';

  return (
    <>
      <p>
        <Link href="/compte">← Mes passes</Link>
      </p>
      <h1>Prendre un pass</h1>
      <section className="cl-card">
        <BuyForm passes={passes} boxes={boxes} initialBox={box} initialTier={tier} />
      </section>
      <p className="cl-muted">
        Paiement par Wave ou Orange Money sur la page sécurisée de NabooPay. Le pass s’active dès que le paiement est
        confirmé, sans abonnement : rien n’est prélevé ensuite.
      </p>
    </>
  );
}

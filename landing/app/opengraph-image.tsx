import { ImageResponse } from 'next/og';

// The preview shown when the page is shared (WhatsApp, Facebook, X),
// rendered once at build time.
export const dynamic = 'force-static';
export const alt = 'Kergui : qui est sur votre Wi-Fi ?';
export const size = { width: 1200, height: 630 };
export const contentType = 'image/png';

export default function OpengraphImage() {
  return new ImageResponse(
    (
      <div
        style={{
          width: '100%',
          height: '100%',
          display: 'flex',
          flexDirection: 'column',
          justifyContent: 'center',
          padding: '80px',
          background: '#0B6E4F',
          color: '#fff',
          fontFamily: 'sans-serif',
        }}
      >
        <div style={{ display: 'flex', alignItems: 'center', gap: 18, fontSize: 44, fontWeight: 800 }}>
          <div style={{ width: 28, height: 28, borderRadius: 14, background: '#F2A900' }} />
          Kergui
        </div>
        <div style={{ fontSize: 72, fontWeight: 800, marginTop: 30, lineHeight: 1.1 }}>Qui est sur votre Wi-Fi ?</div>
        <div style={{ fontSize: 34, marginTop: 24, opacity: 0.9 }}>
          Voyez, autorisez ou bloquez chaque appareil de votre box fibre.
        </div>
      </div>
    ),
    size,
  );
}

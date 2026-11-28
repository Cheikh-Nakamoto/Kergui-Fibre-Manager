import { BackIcon, BellIcon, ChartIcon, DevicesIcon, GearIcon, TabDevicesIcon, WifiIcon } from './icons';

// Drawn phone screens of the app (decorative: each frame is one labelled
// image for screen readers).

export function PhoneFrame({
  label,
  className = '',
  children,
  ...rest
}: { label: string } & React.ComponentProps<'div'>) {
  return (
    <div role="img" aria-label={label} className={`ph ${className}`} {...rest}>
      <div className="ph-screen">
        <div className="ph-notch" />
        {children}
      </div>
    </div>
  );
}

function AppBar({ title, back, bell = !back }: { title: string; back?: boolean; bell?: boolean }) {
  return (
    <div className={`ph-bar${back ? ' small' : ''}`}>
      <span className="ph-bar-title">
        {back ? <BackIcon size={16} width={2.2} /> : null}
        {title}
      </span>
      {bell ? <BellIcon size={16} /> : null}
    </div>
  );
}

const tabs = [
  { key: 'devices', label: 'Appareils', Icon: TabDevicesIcon },
  { key: 'usage', label: 'Conso', Icon: ChartIcon },
  { key: 'wifi', label: 'Wi-Fi', Icon: WifiIcon },
  { key: 'settings', label: 'Réglages', Icon: GearIcon },
] as const;

function TabBar({ active }: { active?: (typeof tabs)[number]['key'] }) {
  return (
    <div className="ph-tabs">
      {tabs.map(({ key, label, Icon }) => (
        <span key={key} className={key === active ? 'on' : undefined}>
          <Icon size={16} />
          {label}
        </span>
      ))}
    </div>
  );
}

const devices = [
  { name: 'Téléphone de Awa', ip: '192.168.1.11', mac: '8a:26:••:••:3c:df' },
  { name: 'TV du salon', ip: '192.168.1.3', mac: 'fc:18:••:••:16:38' },
  { name: 'Tablette-Visiteur', ip: '192.168.1.77', mac: 'a6:fb:••:••:03:ee', fresh: true },
  { name: 'PC du bureau', ip: '192.168.1.4', mac: '90:81:••:••:94:09' },
  { name: 'iPhone de Fatou', ip: '192.168.1.2', mac: '76:8d:••:••:dd:ad' },
  { name: 'Tablette des enfants', ip: '192.168.1.5', mac: 'd2:b9:••:••:31:fd' },
];

export function DevicesScreen() {
  return (
    <>
      <AppBar title="Appareils" />
      <div className="ph-body tight">
        <div className="ph-search">⌕&nbsp;&nbsp;Rechercher</div>
        <span className="ph-note">6 connectés · 1 bloqué · 1 nouveau</span>
        <div className="ph-seg">
          <span className="on">Appareils (6)</span>
          <span>Bloqués (1)</span>
        </div>
        <div className="ph-list">
          {devices.map((d) => (
            <div key={d.name} className={`ph-dev${d.fresh ? ' fresh pulse' : ''}`}>
              <span className="ph-avatar">
                <DevicesIcon size={14} color="#0B3B29" width={2.2} />
              </span>
              <span className="ph-dev-text">
                <span>{d.name}</span>
                <small>
                  {d.ip} · {d.mac}
                </small>
              </span>
              {d.fresh ? <span className="ph-new">NOUVEAU</span> : null}
            </div>
          ))}
        </div>
      </div>
      <TabBar active="devices" />
    </>
  );
}

function Toggle({ on }: { on?: boolean }) {
  return <span aria-hidden="true" className={`ph-toggle${on ? ' on' : ''}`} />;
}

export function WifiScreen() {
  const nets = [
    { name: 'Maison', meta: '2.4GHz · WPA2/WPA3-SAE', on: true },
    { name: 'Invités', meta: '2.4GHz · désactivé', on: false },
  ];
  return (
    <>
      <AppBar title="Wi-Fi" />
      <div className="ph-body">
        {nets.map((n) => (
          <div key={n.name} className="ph-panel">
            <div className="ph-line">
              <span className="ph-dev-text">
                <span>{n.name}</span>
                <small>{n.meta}</small>
              </span>
              <Toggle on={n.on} />
            </div>
            <div className="ph-line gap">
              <span className="ph-field">{n.name}</span>
              <span className="ph-chip">Renommer</span>
            </div>
            <span className="ph-link">⚿&nbsp; Changer le mot de passe</span>
          </div>
        ))}
      </div>
      <TabBar active="wifi" />
    </>
  );
}

export function UsageScreen() {
  const rows = [
    { name: 'PC du bureau', total: '252 Go', w: 100, up: '1,3 Go', down: '4,5 Go' },
    { name: 'TV du salon', total: '91 Go', w: 36, up: '556 Mo', down: '11 Go' },
    { name: 'Téléphone de Awa', total: '75 Go', w: 30, up: '255 Mo', down: '4,1 Go' },
    { name: 'iPhone de Fatou', total: '72 Go', w: 28, up: '2,5 Go', down: '5,5 Go' },
  ];
  return (
    <>
      <AppBar title="Conso" />
      <div className="ph-body roomy">
        <div className="ph-month">‹&nbsp;&nbsp;&nbsp;novembre 2026&nbsp;&nbsp;&nbsp;›</div>
        <strong className="ph-total">Ce mois-ci : 1,2 To pour toute la maison</strong>
        {rows.map((r) => (
          <div key={r.name} className="ph-use">
            <div className="ph-line">
              <span>{r.name}</span>
              <span>{r.total}</span>
            </div>
            <div className="ph-meter">
              <i style={{ width: `${r.w}%` }} />
            </div>
            <small>
              ↑ {r.up} envoyés · ↓ {r.down} reçus
            </small>
          </div>
        ))}
      </div>
      <TabBar active="usage" />
    </>
  );
}

export function WhatsAppScreen() {
  return (
    <>
      <div className="ph-wa-head">
        <span aria-hidden="true" className="ph-wa-avatar">
          K
        </span>
        <span className="ph-dev-text">
          <strong>Kergui</strong>
          <small>WhatsApp</small>
        </span>
      </div>
      <div className="ph-wa">
        <div className="msg m1 in-msg">
          Kergui : un appareil à vérifier vient de rejoindre votre Wi-Fi.
          <br />
          1. Une tablette Samsung, arrivée à 21 h 05
          <br />
          Répondez 1 pour le bloquer, ou OK pour l’autoriser.
        </div>
        <div className="msg m2 out-msg">1</div>
        <div className="msg m3 in-msg">✅ La tablette Samsung arrivée à 21 h 05 est bloquée.</div>
      </div>
    </>
  );
}

export function DeviceScreen() {
  const months = ['juin', 'juil.', 'août', 'sept.', 'oct.', 'nov.'];
  return (
    <>
      <AppBar title="Tablette des enfants" back />
      <div className="ph-body detail">
        <span className="ph-meta">
          MAC d2:b9:••:••:31:fd
          <br />
          IP 192.168.1.5
          <br />
          Wi-Fi Maison
        </span>
        <span className="ph-ok">Autorisé</span>
        <small>Le foyer connaît cet appareil : il n’est plus signalé quand il rejoint le Wi-Fi.</small>
        <span className="ph-h">Consommation sur 6 mois</span>
        <div className="ph-chart">
          {months.map((m, i) => (
            <div key={m}>
              <i className={i === months.length - 1 ? 'on' : undefined} />
              <span>{m}</span>
            </div>
          ))}
        </div>
        <div className="ph-input">
          <small>Nom personnalisé</small>
          <span>Tablette des enfants</span>
        </div>
        <div className="ph-danger">⊘&nbsp; Bloquer</div>
        <div className="ph-danger ghost">Couper l’accès</div>
      </div>
    </>
  );
}

export function MoreScreen() {
  const groups = [
    {
      title: 'Contrôle à distance',
      items: [
        { icon: <DevicesIcon size={13} color="#0B3B29" width={2.2} />, tone: 'green', name: 'Relais à la maison', text: 'Pour gérer la box quand vous n’êtes pas chez vous.' },
        { icon: '⌂', tone: 'green', name: 'Foyer et membres', text: 'Invitez les membres du foyer et choisissez qui peut voir ou contrôler la box.' },
      ],
    },
    {
      title: 'Alertes WhatsApp',
      items: [
        { icon: '✆', tone: 'amber', name: 'Recevoir les alertes sur WhatsApp', text: 'Un message à chaque nouvel appareil.' },
        { icon: '1', tone: 'amber', name: 'Répondre « 1 » pour bloquer', text: 'Répondez « 1 » à une alerte pour bloquer l’appareil.' },
      ],
    },
    {
      title: 'Automatisation',
      items: [{ icon: '⛨', tone: 'red', name: 'Rejet automatique', text: 'Les appareils que vous n’avez pas autorisés sont refusés tout seuls.' }],
    },
  ];
  return (
    <>
      <AppBar title="Plus de fonctionnalités" back />
      <div className="ph-body roomy">
        {groups.map((g) => (
          <div key={g.title} className="ph-group">
            <small>{g.title}</small>
            {g.items.map((it) => (
              <div key={it.name} className="ph-feat">
                <span className={`ph-avatar ${it.tone}`}>{it.icon}</span>
                <span className="ph-dev-text">
                  <span>{it.name}</span>
                  <small>{it.text}</small>
                </span>
              </div>
            ))}
          </div>
        ))}
      </div>
    </>
  );
}

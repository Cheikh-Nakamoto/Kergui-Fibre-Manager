// The small line icons of the design (24×24, stroke = currentColor unless given).

type P = { size?: number; color?: string; width?: number };

function Svg({ size = 16, color = 'currentColor', width = 2, children }: P & { children: React.ReactNode }) {
  return (
    <svg
      aria-hidden="true"
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke={color}
      strokeWidth={width}
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      {children}
    </svg>
  );
}

export function WifiIcon(p: P & { dot?: boolean }) {
  return (
    <Svg {...p}>
      <path d="M2 8.5a15 15 0 0 1 20 0" />
      <path d="M5.5 12a10 10 0 0 1 13 0" />
      <path d="M9 15.5a5 5 0 0 1 6 0" />
      {p.dot ? <circle cx="12" cy="19" r="1.2" fill={p.color ?? 'currentColor'} /> : null}
    </Svg>
  );
}

export function DownloadIcon(p: P) {
  return (
    <Svg {...p}>
      <path d="M12 3v12" />
      <path d="m7 10 5 5 5-5" />
      <path d="M5 21h14" />
    </Svg>
  );
}

export function PlayIcon(p: P) {
  return (
    <Svg {...p}>
      <path d="M5 3.5v17l14-8.5z" />
    </Svg>
  );
}

export function BellIcon(p: P) {
  return (
    <Svg {...p}>
      <path d="M6 8a6 6 0 0 1 12 0c0 7 3 9 3 9H3s3-2 3-9" />
      <path d="M10.3 21a1.9 1.9 0 0 0 3.4 0" />
    </Svg>
  );
}

export function DevicesIcon(p: P) {
  return (
    <Svg {...p}>
      <rect x="3" y="4" width="8" height="6" rx="1" />
      <path d="M5 10v5h4" />
      <rect x="11" y="13" width="10" height="7" rx="1" />
    </Svg>
  );
}

export function TabDevicesIcon(p: P) {
  return (
    <Svg {...p}>
      <rect x="3" y="5" width="11" height="12" rx="1" />
      <rect x="16" y="9" width="5" height="9" rx="1" />
    </Svg>
  );
}

export function ChartIcon(p: P) {
  return (
    <Svg {...p}>
      <path d="M5 20V9" />
      <path d="M10 20V4" />
      <path d="M15 20v-7" />
      <path d="M20 20v-4" />
    </Svg>
  );
}

export function GearIcon(p: P) {
  return (
    <Svg {...p}>
      <circle cx="12" cy="12" r="3" />
      <path d="M12 2v3M12 19v3M2 12h3M19 12h3M4.9 4.9l2.1 2.1M17 17l2.1 2.1M4.9 19.1 7 17M17 7l2.1-2.1" />
    </Svg>
  );
}

export function BackIcon(p: P) {
  return (
    <Svg {...p}>
      <path d="M19 12H5" />
      <path d="m11 18-6-6 6-6" />
    </Svg>
  );
}

export function ChevronIcon({ dir, ...p }: P & { dir: 'left' | 'right' }) {
  return <Svg {...p}>{dir === 'left' ? <path d="m15 18-6-6 6-6" /> : <path d="m9 18 6-6-6-6" />}</Svg>;
}

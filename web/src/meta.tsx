// Small helpers for metadata display (docs/metadata.md). Everything here tolerates missing data.

const ISO: Record<string, string> = {
  "united states": "US", "united kingdom": "GB", canada: "CA", germany: "DE", france: "FR", spain: "ES", italy: "IT",
  netherlands: "NL", belgium: "BE", switzerland: "CH", austria: "AT", sweden: "SE", norway: "NO", denmark: "DK",
  finland: "FI", ireland: "IE", portugal: "PT", poland: "PL", australia: "AU", "new zealand": "NZ", japan: "JP",
  china: "CN", india: "IN", brazil: "BR", mexico: "MX", argentina: "AR", chile: "CL", turkey: "TR", greece: "GR",
  "south korea": "KR", singapore: "SG", "united arab emirates": "AE", qatar: "QA", "south africa": "ZA", israel: "IL",
  "czech republic": "CZ", hungary: "HU", iceland: "IS", luxembourg: "LU", thailand: "TH", malaysia: "MY",
};

/** Flag emoji from the country name, "" when the name is not in the small table. */
export function flag(country: string | undefined): string {
  const c = country ? ISO[country.trim().toLowerCase()] : undefined;
  return c ? String.fromCodePoint(...[...c].map((ch) => 0x1f1e6 + ch.charCodeAt(0) - 65)) : "";
}

/** "BOEING 737-800" -> "737-800" (drops the manufacturer word, caps at 10 chars); short enough for a map label. */
export function shortType(type: string | undefined): string {
  if (!type) return "";
  const parts = type.trim().split(/\s+/);
  const s = parts.length > 1 ? parts.slice(1).join(" ") : parts[0]!;
  return s.length > 10 ? s.slice(0, 10) : s;
}

/** Last scan's labelled carriers, for the spectrum hover tooltip (written by the Tools scan, read on hover). */
const labelled: { hz: number; label: string }[] = [];
export function setPeakLabels(peaks: { freq_hz: number; label?: string }[]): void {
  labelled.length = 0;
  for (const p of peaks.slice(0, 500)) if (p.label) labelled.push({ hz: p.freq_hz, label: p.label });
}
export function peakLabelNear(hz: number, tolHz: number): string | null {
  let best: string | null = null;
  let bd = tolHz;
  for (const p of labelled) {
    const d = Math.abs(p.hz - hz);
    if (d <= bd) {
      bd = d;
      best = p.label;
    }
  }
  return best;
}

const SOURCES = {
  aircraft: [
    ["Airlines", "OpenFlights (ODbL 1.0)"],
    ["US registry", "FAA releasable aircraft database (public domain)"],
  ],
  satellites: [
    ["Satellites and transmitters", "SatNOGS DB (CC BY-SA 4.0)"],
    ["Orbits", "CelesTrak"],
  ],
} as const;

export function DataSources({ kind }: { kind: keyof typeof SOURCES }) {
  return (
    <div className="dim small datasrc" title="Datasets are downloaded in bulk and queried offline (make metadata)">
      Data sources: {SOURCES[kind].map(([k, v]) => `${k}: ${v}`).join(" · ")}
    </div>
  );
}

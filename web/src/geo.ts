// Great-circle helpers.
const R_NMI = 3440.065; // mean earth radius in nautical miles
const rad = (d: number) => (d * Math.PI) / 180;

export interface LatLon {
  lat: number;
  lon: number;
}

/** Haversine distance in nautical miles. */
export function rangeNmi(a: LatLon, b: LatLon): number {
  const dLat = rad(b.lat - a.lat);
  const dLon = rad(b.lon - a.lon);
  const h = Math.sin(dLat / 2) ** 2 + Math.cos(rad(a.lat)) * Math.cos(rad(b.lat)) * Math.sin(dLon / 2) ** 2;
  return 2 * R_NMI * Math.asin(Math.min(1, Math.sqrt(h)));
}

/** Initial bearing a -> b in degrees (0-360). */
export function bearingDeg(a: LatLon, b: LatLon): number {
  const y = Math.sin(rad(b.lon - a.lon)) * Math.cos(rad(b.lat));
  const x =
    Math.cos(rad(a.lat)) * Math.sin(rad(b.lat)) - Math.sin(rad(a.lat)) * Math.cos(rad(b.lat)) * Math.cos(rad(b.lon - a.lon));
  return (((Math.atan2(y, x) * 180) / Math.PI) % 360 + 360) % 360;
}

export const NMI_M = 1852;

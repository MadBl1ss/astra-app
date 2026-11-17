// Where the two ends of the beam are. Both are rough on purpose: the globe
// shows "from about here to that country", not an address.

/** Latitude and longitude in degrees. */
export type Place = readonly [number, number];

// The city where a country's servers usually stand: the capital, or the
// hosting hub when that is a different city (Frankfurt, Milan, New York).
const COUNTRIES: Record<string, Place> = {
  AD: [42.5, 1.5], AE: [25.2, 55.3], AL: [41.3, 19.8], AM: [40.2, 44.5], AO: [-8.8, 13.2],
  AR: [-34.6, -58.4], AT: [48.2, 16.4], AU: [-33.9, 151.2], AZ: [40.4, 49.9], BA: [43.9, 18.4],
  BD: [23.8, 90.4], BE: [50.8, 4.4], BG: [42.7, 23.3], BH: [26.2, 50.6], BN: [4.9, 114.9],
  BO: [-16.5, -68.1], BR: [-23.5, -46.6], BS: [25.0, -77.4], BY: [53.9, 27.6], BZ: [17.3, -88.2],
  CA: [43.7, -79.4], CH: [47.4, 8.5], CL: [-33.4, -70.7], CN: [31.2, 121.5], CO: [4.7, -74.1],
  CR: [9.9, -84.1], CU: [23.1, -82.4], CY: [35.2, 33.4], CZ: [50.1, 14.4], DE: [50.1, 8.7],
  DK: [55.7, 12.6], DO: [18.5, -69.9], DZ: [36.8, 3.1], EC: [-0.2, -78.5], EE: [59.4, 24.8],
  EG: [30.0, 31.2], ES: [40.4, -3.7], ET: [9.0, 38.7], FI: [60.2, 24.9], FR: [48.9, 2.3],
  GB: [51.5, -0.1], GE: [41.7, 44.8], GH: [5.6, -0.2], GI: [36.1, -5.4], GL: [64.2, -51.7],
  GR: [38.0, 23.7], GT: [14.6, -90.5], HK: [22.3, 114.2], HN: [14.1, -87.2], HR: [45.8, 16.0],
  HU: [47.5, 19.0], ID: [-6.2, 106.8], IE: [53.3, -6.3], IL: [32.1, 34.8], IM: [54.2, -4.5],
  IN: [19.1, 72.9], IQ: [33.3, 44.4], IR: [35.7, 51.4], IS: [64.1, -21.9], IT: [45.5, 9.2],
  JE: [49.2, -2.1], JM: [18.0, -76.8], JO: [31.9, 35.9], JP: [35.7, 139.7], KE: [-1.3, 36.8],
  KG: [42.9, 74.6], KH: [11.6, 104.9], KR: [37.6, 127.0], KW: [29.4, 48.0], KZ: [43.2, 76.9],
  LA: [18.0, 102.6], LB: [33.9, 35.5], LI: [47.1, 9.5], LK: [6.9, 79.9], LT: [54.7, 25.3],
  LU: [49.6, 6.1], LV: [56.9, 24.1], MA: [33.6, -7.6], MC: [43.7, 7.4], MD: [47.0, 28.9],
  ME: [42.4, 19.3], MK: [42.0, 21.4], MM: [16.8, 96.2], MN: [47.9, 106.9], MO: [22.2, 113.5],
  MT: [35.9, 14.5], MX: [19.4, -99.1], MY: [3.1, 101.7], NG: [6.5, 3.4], NI: [12.1, -86.3],
  NL: [52.4, 4.9], NO: [59.9, 10.8], NP: [27.7, 85.3], NZ: [-36.8, 174.8], OM: [23.6, 58.4],
  PA: [9.0, -79.5], PE: [-12.0, -77.0], PH: [14.6, 121.0], PK: [24.9, 67.0], PL: [52.2, 21.0],
  PR: [18.4, -66.1], PT: [38.7, -9.1], PY: [-25.3, -57.6], QA: [25.3, 51.5], RO: [44.4, 26.1],
  RS: [44.8, 20.5], RU: [55.8, 37.6], SA: [24.7, 46.7], SE: [59.3, 18.1], SG: [1.3, 103.8],
  SI: [46.1, 14.5], SK: [48.1, 17.1], SN: [14.7, -17.4], SV: [13.7, -89.2], TH: [13.8, 100.5],
  TJ: [38.6, 68.8], TM: [37.9, 58.4], TN: [36.8, 10.2], TR: [41.0, 29.0], TT: [10.7, -61.5],
  TW: [25.0, 121.5], TZ: [-6.8, 39.3], UA: [50.5, 30.5], US: [40.7, -74.0], UY: [-34.9, -56.2],
  UZ: [41.3, 69.2], VE: [10.5, -66.9], VN: [10.8, 106.7], ZA: [-26.2, 28.0],
};

// Subscriptions write "UK" as often as "GB".
const ALIASES: Record<string, string> = { UK: 'GB' };

/** The place of a country by its two-letter code, or null when it is not in the table. */
export function countryPlace(code: string | null): Place | null {
  if (!code) return null;
  const upper = code.toUpperCase();
  return COUNTRIES[ALIASES[upper] ?? upper] ?? null;
}

// The city a time zone is named after. The system's time zone is the only
// thing the window knows about where the computer is without asking anyone:
// a lookup by IP would be a request past the tunnel, and under the kill
// switch it would not go through at all.
const ZONES: Record<string, Place> = {
  'Europe/Kaliningrad': [54.7, 20.5], 'Europe/Moscow': [55.8, 37.6], 'Europe/Simferopol': [45.0, 34.1],
  'Europe/Kirov': [58.6, 49.7], 'Europe/Volgograd': [48.7, 44.5], 'Europe/Astrakhan': [46.3, 48.0],
  'Europe/Saratov': [51.5, 46.0], 'Europe/Ulyanovsk': [54.3, 48.4], 'Europe/Samara': [53.2, 50.2],
  'Asia/Yekaterinburg': [56.8, 60.6], 'Asia/Omsk': [55.0, 73.4], 'Asia/Novosibirsk': [55.0, 82.9],
  'Asia/Barnaul': [53.3, 83.8], 'Asia/Tomsk': [56.5, 85.0], 'Asia/Novokuznetsk': [53.8, 87.1],
  'Asia/Krasnoyarsk': [56.0, 92.9], 'Asia/Irkutsk': [52.3, 104.3], 'Asia/Chita': [52.0, 113.5],
  'Asia/Yakutsk': [62.0, 129.7], 'Asia/Khandyga': [62.7, 135.6], 'Asia/Vladivostok': [43.1, 131.9],
  'Asia/Ust-Nera': [64.6, 143.2], 'Asia/Sakhalin': [47.0, 142.7], 'Asia/Magadan': [59.6, 150.8],
  'Asia/Srednekolymsk': [67.5, 153.7], 'Asia/Kamchatka': [53.0, 158.7], 'Asia/Anadyr': [64.7, 177.5],

  'Europe/Minsk': [53.9, 27.6], 'Europe/Kyiv': [50.5, 30.5], 'Europe/Kiev': [50.5, 30.5],
  'Europe/Chisinau': [47.0, 28.9], 'Asia/Almaty': [43.2, 76.9], 'Asia/Qyzylorda': [44.9, 65.5],
  'Asia/Qostanay': [53.2, 63.6], 'Asia/Aqtobe': [50.3, 57.2], 'Asia/Aqtau': [43.7, 51.2],
  'Asia/Atyrau': [47.1, 51.9], 'Asia/Oral': [51.2, 51.4], 'Asia/Tashkent': [41.3, 69.2],
  'Asia/Samarkand': [39.7, 67.0], 'Asia/Bishkek': [42.9, 74.6], 'Asia/Dushanbe': [38.6, 68.8],
  'Asia/Ashgabat': [37.9, 58.4], 'Asia/Baku': [40.4, 49.9], 'Asia/Yerevan': [40.2, 44.5],
  'Asia/Tbilisi': [41.7, 44.8],

  'Europe/London': [51.5, -0.1], 'Europe/Dublin': [53.3, -6.3], 'Europe/Lisbon': [38.7, -9.1],
  'Europe/Madrid': [40.4, -3.7], 'Europe/Paris': [48.9, 2.3], 'Europe/Brussels': [50.8, 4.4],
  'Europe/Amsterdam': [52.4, 4.9], 'Europe/Berlin': [52.5, 13.4], 'Europe/Zurich': [47.4, 8.5],
  'Europe/Rome': [41.9, 12.5], 'Europe/Vienna': [48.2, 16.4], 'Europe/Prague': [50.1, 14.4],
  'Europe/Warsaw': [52.2, 21.0], 'Europe/Budapest': [47.5, 19.0], 'Europe/Bucharest': [44.4, 26.1],
  'Europe/Sofia': [42.7, 23.3], 'Europe/Athens': [38.0, 23.7], 'Europe/Istanbul': [41.0, 29.0],
  'Europe/Belgrade': [44.8, 20.5], 'Europe/Zagreb': [45.8, 16.0], 'Europe/Helsinki': [60.2, 24.9],
  'Europe/Stockholm': [59.3, 18.1], 'Europe/Oslo': [59.9, 10.8], 'Europe/Copenhagen': [55.7, 12.6],
  'Europe/Riga': [56.9, 24.1], 'Europe/Vilnius': [54.7, 25.3], 'Europe/Tallinn': [59.4, 24.8],

  'Asia/Jerusalem': [31.8, 35.2], 'Asia/Tel_Aviv': [32.1, 34.8], 'Asia/Dubai': [25.2, 55.3],
  'Asia/Riyadh': [24.7, 46.7], 'Asia/Qatar': [25.3, 51.5], 'Asia/Tehran': [35.7, 51.4],
  'Asia/Karachi': [24.9, 67.0], 'Asia/Kolkata': [28.6, 77.2], 'Asia/Calcutta': [28.6, 77.2],
  'Asia/Dhaka': [23.8, 90.4], 'Asia/Bangkok': [13.8, 100.5], 'Asia/Ho_Chi_Minh': [10.8, 106.7],
  'Asia/Saigon': [10.8, 106.7], 'Asia/Jakarta': [-6.2, 106.8], 'Asia/Singapore': [1.3, 103.8],
  'Asia/Kuala_Lumpur': [3.1, 101.7], 'Asia/Manila': [14.6, 121.0], 'Asia/Hong_Kong': [22.3, 114.2],
  'Asia/Shanghai': [31.2, 121.5], 'Asia/Taipei': [25.0, 121.5], 'Asia/Seoul': [37.6, 127.0],
  'Asia/Tokyo': [35.7, 139.7], 'Asia/Ulaanbaatar': [47.9, 106.9],

  'America/New_York': [40.7, -74.0], 'America/Toronto': [43.7, -79.4], 'America/Chicago': [41.9, -87.6],
  'America/Denver': [39.7, -105.0], 'America/Phoenix': [33.4, -112.1], 'America/Los_Angeles': [34.1, -118.2],
  'America/Vancouver': [49.3, -123.1], 'America/Anchorage': [61.2, -149.9], 'America/Mexico_City': [19.4, -99.1],
  'America/Bogota': [4.7, -74.1], 'America/Lima': [-12.0, -77.0], 'America/Santiago': [-33.4, -70.7],
  'America/Sao_Paulo': [-23.5, -46.6], 'America/Argentina/Buenos_Aires': [-34.6, -58.4],
  'America/Buenos_Aires': [-34.6, -58.4], 'Pacific/Honolulu': [21.3, -157.9],

  'Africa/Cairo': [30.0, 31.2], 'Africa/Casablanca': [33.6, -7.6], 'Africa/Lagos': [6.5, 3.4],
  'Africa/Nairobi': [-1.3, 36.8], 'Africa/Johannesburg': [-26.2, 28.0],
  'Australia/Perth': [-31.9, 115.9], 'Australia/Adelaide': [-34.9, 138.6], 'Australia/Brisbane': [-27.5, 153.0],
  'Australia/Sydney': [-33.9, 151.2], 'Australia/Melbourne': [-37.8, 145.0], 'Pacific/Auckland': [-36.8, 174.8],
};

/**
 * Roughly where this computer is, from the time zone the system is set to.
 * A zone that is not in the table still gives the right meridian: an hour
 * of offset is fifteen degrees.
 */
export function homePlace(zone: string, offsetMinutes: number): Place {
  return ZONES[zone] ?? [45, Math.max(-180, Math.min(180, (-offsetMinutes / 60) * 15))];
}

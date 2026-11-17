// Cities to choose the near end of the globe's beam from, in Settings. The
// daemon finds the place by IP by itself; this list is for when the address
// lies (providers often "live" in another city) or the user would rather
// not ask a service at all. Big Russian cities first, then the places
// people from them most often live in or travel to.

export interface City {
  id: string;
  name: string;
  lat: number;
  lon: number;
  /** ISO code, upper case. */
  country: string;
}

const list: [string, string, number, number, string][] = [
  ['msk', 'Москва', 55.7558, 37.6173, 'RU'],
  ['spb', 'Санкт-Петербург', 59.9343, 30.3351, 'RU'],
  ['nsk', 'Новосибирск', 55.0084, 82.9357, 'RU'],
  ['ekb', 'Екатеринбург', 56.8389, 60.6057, 'RU'],
  ['kzn', 'Казань', 55.7963, 49.1088, 'RU'],
  ['nnov', 'Нижний Новгород', 56.2965, 43.9361, 'RU'],
  ['chel', 'Челябинск', 55.1644, 61.4368, 'RU'],
  ['sam', 'Самара', 53.1959, 50.1002, 'RU'],
  ['omsk', 'Омск', 54.9885, 73.3242, 'RU'],
  ['rnd', 'Ростов-на-Дону', 47.2357, 39.7015, 'RU'],
  ['ufa', 'Уфа', 54.7388, 55.9721, 'RU'],
  ['krsk', 'Красноярск', 56.0153, 92.8932, 'RU'],
  ['vrn', 'Воронеж', 51.6608, 39.2003, 'RU'],
  ['perm', 'Пермь', 58.0105, 56.2502, 'RU'],
  ['vlg', 'Волгоград', 48.708, 44.5133, 'RU'],
  ['krd', 'Краснодар', 45.0355, 38.9753, 'RU'],
  ['sochi', 'Сочи', 43.6028, 39.7342, 'RU'],
  ['kgd', 'Калининград', 54.7104, 20.4522, 'RU'],
  ['irk', 'Иркутск', 52.287, 104.305, 'RU'],
  ['khv', 'Хабаровск', 48.4802, 135.0719, 'RU'],
  ['vvo', 'Владивосток', 43.1155, 131.8855, 'RU'],
  ['mmk', 'Мурманск', 68.9585, 33.0827, 'RU'],

  ['ala', 'Алматы', 43.222, 76.8512, 'KZ'],
  ['ams', 'Амстердам', 52.3676, 4.9041, 'NL'],
  ['ayt', 'Анталья', 36.8969, 30.7133, 'TR'],
  ['nqz', 'Астана', 51.1694, 71.4491, 'KZ'],
  ['gyd', 'Баку', 40.4093, 49.8671, 'AZ'],
  ['bkk', 'Бангкок', 13.7563, 100.5018, 'TH'],
  ['beg', 'Белград', 44.7866, 20.4489, 'RS'],
  ['ber', 'Берлин', 52.52, 13.405, 'DE'],
  ['fru', 'Бишкек', 42.8746, 74.5698, 'KG'],
  ['bue', 'Буэнос-Айрес', -34.6037, -58.3816, 'AR'],
  ['vie', 'Вена', 48.2082, 16.3738, 'AT'],
  ['vno', 'Вильнюс', 54.6872, 25.2797, 'LT'],
  ['dps', 'Денпасар (Бали)', -8.6705, 115.2126, 'ID'],
  ['dxb', 'Дубай', 25.2048, 55.2708, 'AE'],
  ['evn', 'Ереван', 40.1792, 44.4991, 'AM'],
  ['kiv', 'Кишинёв', 47.0105, 28.8638, 'MD'],
  ['iev', 'Киев', 50.4501, 30.5234, 'UA'],
  ['lon', 'Лондон', 51.5074, -0.1278, 'GB'],
  ['lax', 'Лос-Анджелес', 34.0522, -118.2437, 'US'],
  ['mad', 'Мадрид', 40.4168, -3.7038, 'ES'],
  ['msq', 'Минск', 53.9006, 27.559, 'BY'],
  ['nyc', 'Нью-Йорк', 40.7128, -74.006, 'US'],
  ['par', 'Париж', 48.8566, 2.3522, 'FR'],
  ['pek', 'Пекин', 39.9042, 116.4074, 'CN'],
  ['prg', 'Прага', 50.0755, 14.4378, 'CZ'],
  ['hkt', 'Пхукет', 7.8804, 98.3923, 'TH'],
  ['rix', 'Рига', 56.9496, 24.1052, 'LV'],
  ['rom', 'Рим', 41.9028, 12.4964, 'IT'],
  ['ist', 'Стамбул', 41.0082, 28.9784, 'TR'],
  ['tll', 'Таллин', 59.437, 24.7536, 'EE'],
  ['tas', 'Ташкент', 41.2995, 69.2401, 'UZ'],
  ['tlv', 'Тель-Авив', 32.0853, 34.7818, 'IL'],
  ['tbs', 'Тбилиси', 41.7151, 44.8271, 'GE'],
  ['tyo', 'Токио', 35.6762, 139.6503, 'JP'],
  ['yto', 'Торонто', 43.6532, -79.3832, 'CA'],
  ['hel', 'Хельсинки', 60.1699, 24.9384, 'FI'],
  ['ruh', 'Эр-Рияд', 24.7136, 46.6753, 'SA'],
];

export const CITIES: readonly City[] = list.map(([id, name, lat, lon, country]) => ({ id, name, lat, lon, country }));

/** The city of the list a place was chosen from, if it was. */
export function cityOf(place: { lat: number; lon: number; city: string } | null): City | undefined {
  if (!place) return undefined;
  return CITIES.find((c) => c.name === place.city && Math.abs(c.lat - place.lat) < 0.01 && Math.abs(c.lon - place.lon) < 0.01);
}

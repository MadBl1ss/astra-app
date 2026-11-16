// Every string the interface shows lives here, so that a second language is
// one more file of the same shape. Tone: short, plain, informal "ты", no
// jargon and no exclamation marks.

/** Russian plural: plural(5, ['сервер', 'сервера', 'серверов']). */
export function plural(n: number, forms: readonly [string, string, string]): string {
  const abs = Math.abs(n) % 100;
  const last = abs % 10;
  if (abs > 10 && abs < 20) return forms[2];
  if (last > 1 && last < 5) return forms[1];
  if (last === 1) return forms[0];
  return forms[2];
}

const conns = ['соединение', 'соединения', 'соединений'] as const;

export const ru = {
  app: {
    name: 'Стрелка',
    minimize: 'Свернуть',
    close: 'Закрыть',
  },

  nav: {
    label: 'Разделы',
    home: 'Главная',
    servers: 'Серверы',
    rules: 'Правила',
    journal: 'Журнал',
    settings: 'Настройки',
  },

  units: {
    ms: (n: number) => `${n} мс`,
    bps: 'Б/с',
    kbps: 'КБ/с',
    mbps: 'МБ/с',
    gbps: 'ГБ/с',
    sec: 'с',
    min: 'мин',
    hour: 'ч',
    day: 'д',
    decimal: ',',
  },

  // The big word on Home and the line under it.
  state: {
    ok: 'Защищено',
    off: 'Выключено',
    offSub: 'Весь трафик идёт напрямую',
    offEmptySub: 'Чтобы включить VPN, добавь подписку в разделе «Серверы»',
    starting: 'Подключение…',
    stopping: 'Выключение…',
    stoppingSub: 'Трафик возвращается на прямой путь',
    settlingSub: (server: string) => `Проверяю ${server}`,
    degraded: 'Соединение нестабильно',
    degradedSub: (server: string) => `${server} отвечает через раз. VPN работает, но медленно`,
    serverDown: 'Сервер не отвечает',
    serverDownSub: (server: string, silent: string) => `${server} молчит ${silent}. Интернет есть`,
    allDown: 'Серверы не отвечают',
    allDownSub: 'Интернет есть, но ни один сервер не отвечает',
    noInternet: 'Нет интернета',
    noInternetSub: 'VPN заработает, когда сеть вернётся',
    noNetwork: 'Нет сети',
    noNetworkSub: 'Компьютер не подключён к сети. VPN заработает, когда она вернётся',
    recovering: 'Перезапуск…',
    recoveringSub: 'VPN остановился и поднимается снова',
    failed: 'VPN не запускается',
    failedSub: 'Причина — в журнале',
    helperDown: 'Часть серверов недоступна',
    helperDownSub: 'Выбери другой сервер или попробуй снова',
    blocked: 'Интернет закрыт',
    blockedSub: (why: string) => `VPN не запускается: ${why}`,
    blockedSubPlain: 'VPN не работает, замок не пускает трафик мимо него',
    noServer: 'сервер не выбран',
    uptime: (d: string) => `работает ${d}`,
    switchLabel: 'VPN',
  },

  banner: {
    switchTo: (server: string) => `Переключить на ${server}`,
    retry: 'Попробовать снова',
    release: 'Разрешить интернет без VPN',
    updateSubs: 'Обновить подписки',
    journal: 'Подробнее',
    blocked: 'Замок закрыл интернет, чтобы трафик не пошёл мимо VPN.',
    unstable: 'Соединение то появляется, то пропадает.',
  },

  tracks: {
    aria: 'Схема: трафик расходится на путь через VPN и путь напрямую',
    pc: ['Этот', 'компьютер'] as const,
    vpn: 'Через VPN',
    direct: 'Напрямую',
    internet: 'Интернет',
    noServer: 'Сервер не выбран',
    conns: (n: number) => `${n} ${plural(n, conns)}`,
    down: (speed: string) => `↓ ${speed}`,
    up: (speed: string) => `↑ ${speed}`,
    idle: 'Сейчас тихо',
    vpnOff: 'VPN выключен',
    vpnStarting: 'Подключается',
    vpnWaiting: 'Программы из списка VPN ждут сервер',
    vpnAll: 'Сюда идёт весь трафик',
    directOff: 'Сюда идёт весь трафик',
    directGlobal: 'Только локальная сеть',
    noInternet: 'Нет интернета',
    blockedVpn: 'Закрыто замком',
    blockedDirect: 'Закрыто замком',
    blockedDirectLan: 'Закрыто замком, локальная сеть работает',
  },

  quick: {
    server: 'Сервер',
    serverEmpty: 'Нет серверов',
    serverPick: 'Выбери сервер',
    noAnswer: 'нет ответа',
    noPing: '—',
    mode: 'Что идёт через VPN',
    modeRule: 'Только список',
    modeGlobal: 'Весь трафик',
    guard: 'Замок',
    guardOn: 'Включён',
    guardOff: 'Выключен',
    guardIdle: 'Не действует, пока VPN выключен',
    guardBlocking: 'Держит интернет закрытым',
    guardNotReady: 'Пока не защищает',
    guardNotReadyHint:
      'Замок в этой версии ещё не готов. Если VPN остановится, трафик пойдёт напрямую.',
    guardHint:
      'Замок закрывает интернет, если VPN остановился, чтобы трафик не пошёл мимо него.',
    guardSwitch: 'Замок',
  },

  log: {
    title: 'Последние события',
    all: 'Весь журнал',
    empty: 'Пока ничего не происходило',
  },

  noDaemon: {
    title: 'Служба не отвечает',
    body: 'Окно не может связаться со службой Стрелки. Без неё VPN не включить и не выключить.',
    steps: [
      'Если VPN был включён, он, скорее всего, продолжает работать: его держит служба, а не это окно.',
      'Подожди несколько секунд: окно само пробует подключиться заново.',
      'Не помогло — перезагрузи компьютер или переустанови Стрелку.',
    ] as const,
    retry: 'Повторить',
    retrying: 'Подключаюсь…',
    connecting: 'Подключаюсь к службе…',
    outdatedTitle: 'Обнови Стрелку',
    outdatedBody:
      'Окно и служба от разных версий и не понимают друг друга. Установи свежую Стрелку поверх этой.',
    versions: (app: string, daemon: string) => `Окно ${app}, служба ${daemon}`,
  },

  soon: {
    title: 'Скоро',
    servers: 'Здесь будут подписки и серверы: проверка, избранное и порядок запасных.',
    rules: 'Здесь будут списки «Через VPN» и «Напрямую» и живые соединения.',
    journal: 'Здесь будет лента событий с фильтром и отчётом для поддержки.',
    settings: 'Остальные настройки появятся здесь.',
  },

  settings: {
    look: 'Оформление',
    theme: 'Тема',
    themeSystem: 'Как в системе',
    themeDark: 'Тёмная',
    themeLight: 'Светлая',
  },

  tray: {
    open: 'Открыть',
    turnOn: 'Включить VPN',
    turnOff: 'Выключить VPN',
    quit: 'Выход',
    noDaemon: 'Стрелка: служба не отвечает',
    tooltip: (word: string, detail: string | null) => (detail ? `Стрелка: ${word}\n${detail}` : `Стрелка: ${word}`),
  },

  errors: {
    link: 'Служба не ответила. Попробуй ещё раз.',
    dismiss: 'Закрыть',
  },
};

export type Dict = typeof ru;

// Texts of the fake daemon. The real one writes its own (core/internal/
// incident/texts.go); these only have to read the same way in the mock.
export const mock = {
  busy: 'Подожди: Стрелка ещё занята предыдущим действием.',
  restartWhileOff: 'VPN выключен: перезапускать нечего.',
  noServers: 'Нет ни одного сервера. Добавь подписку.',
  noSuchServer: 'Такого сервера нет.',
  badSetting: 'Такой настройки нет.',
  unknownMethod: 'Служба не знает такой команды.',
  modeGlobal: 'Режим: весь трафик через VPN',
  modeRule: 'Режим: через VPN только список',
  stopped: 'VPN выключен',
  selected: (server: string) => `Выбран сервер ${server}`,
  connected: (server: string, ping: number | null) =>
    ping === null ? `VPN подключён: ${server}` : `VPN подключён: ${server}, ${ping} мс`,
  serverDownTitle: (server: string) => `Сервер ${server} не отвечает`,
  serverDownBody: 'Интернет есть, другие серверы работают.',
  serverDownHint: 'Похоже, сервер выключен или перегружен. Попробуй другой.',
  guardOn: 'Замок включён',
  guardOff: 'Замок выключен',
  released: 'Интернет разрешён без VPN до следующего включения',
  subsUpdated: (servers: number) =>
    `Подписка обновлена: ${servers} ${plural(servers, ['сервер', 'сервера', 'серверов'])}`,
  daemonStarted: 'Стрелка запущена',
  recovered: 'Германия 2 не отвечала 3 мин, потом восстановилась',
  noInternet: 'Нет интернета. VPN заработает, когда сеть вернётся',
};

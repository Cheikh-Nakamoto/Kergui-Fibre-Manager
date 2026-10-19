// French is the default and, for now, the only locale. Keys are grouped by screen.
export const fr = {
  'common.cancel': 'Annuler',
  'common.confirm': 'Confirmer',
  'common.retry': 'Réessayer',
  'common.save': 'Enregistrer',

  'welcome.title': 'Kergui',
  'welcome.subtitle': 'Gérez votre box ZTE depuis votre téléphone : appareils connectés, blocage, réseaux Wi-Fi.',
  'welcome.connect': 'Connecter ma box',
  'welcome.demo': 'Essayer la démo',
  'welcome.demoHint': 'La démo utilise une box simulée dans l’app, sans réseau.',
  'welcome.bootError': 'Le moteur de l’app n’a pas démarré : {error}',

  'connect.title': 'Connecter ma box',
  'connect.hint': 'Votre téléphone doit être connecté au Wi-Fi de la box.',
  'connect.address': 'Adresse de la box',
  'connect.username': 'Utilisateur',
  'connect.password': 'Mot de passe',
  'connect.submit': 'Se connecter',
  'connect.trustTitle': 'Certificat de la box',
  'connect.trustBody':
    'Votre box utilise un certificat qu’elle a signé elle-même. Kergui va retenir son empreinte et refusera toute autre box à cette adresse.\n\n{fingerprint}',
  'connect.trust': 'Faire confiance',
  'connect.unreachable': 'Box injoignable à cette adresse. Vérifiez que vous êtes sur son Wi-Fi.',

  'tabs.devices': 'Appareils',
  'tabs.wifi': 'Wi-Fi',
  'tabs.settings': 'Réglages',

  'devices.search': 'Rechercher',
  'devices.empty': 'Aucun appareil.',
  'devices.new': 'Nouveau',
  'devices.blocked': 'Bloqué',
  'devices.offline': 'Hors ligne',
  'devices.summary':
    '{connected} {connected|connecté|connectés} · {blocked} {blocked|bloqué|bloqués} · {new} {new|nouveau|nouveaux}',

  'device.rename': 'Nom personnalisé',
  'device.block': 'Bloquer',
  'device.unblock': 'Débloquer',
  'device.blockYes': 'Oui, bloquer',
  'device.unblockYes': 'Oui, débloquer',
  'device.blockConfirm': 'Bloquer {name} ? L’appareil perdra l’accès à Internet.',
  'device.unblockConfirm': 'Débloquer {name} ?',
  'device.details': 'MAC {mac}\nIP {ip}\nFabricant {vendor}\nWi-Fi {ssid}',

  'wifi.filter': 'Filtrage MAC',
  'wifi.filterOn': 'Activer le filtrage MAC ? Seuls les appareils autorisés pourront se connecter.',
  'wifi.filterOff': 'Désactiver le filtrage MAC ?',
  'wifi.hidden': 'masqué',
  'wifi.disabled': 'désactivé',
  'wifi.newPassword': 'Nouveau mot de passe (8 caractères min.)',
  'wifi.name': 'Nom du réseau',
  'wifi.toggleConfirm': '{action} le réseau « {name} » ? Les appareils connectés dessus seront coupés.',
  'wifi.enable': 'Activer',
  'wifi.disable': 'Désactiver',

  'settings.box': 'Box',
  'settings.demo': 'Mode démo',
  'settings.version': 'Version {app} · moteur {core}',
  'settings.signOut': 'Changer de box',
  'settings.signOutConfirm': 'Oublier cette box ? Vous devrez vous reconnecter.',
  'settings.journal': 'Journal technique',
  'settings.journalHint': 'Ce que l’app a demandé à la box, et ce qu’elle a répondu.',

  'journal.title': 'Journal technique',
  'journal.hint':
    'Les dernières actions de l’app et les réponses de la box. À partager avec le support si quelque chose ne marche pas.',
  'journal.empty': 'Rien pour l’instant. Utilisez l’app, puis revenez ici.',
  'journal.all': 'Tout',
  'journal.problemsOnly': 'Problèmes',
  'journal.share': 'Partager',
  'journal.copied': 'Journal copié.',
  'journal.shareFailed': 'Partage impossible sur cet appareil.',
  'journal.count': '{count} ligne{count|| s}',

  'error.notSupported': 'Pas encore pris en charge sur votre modèle de box.',
  'error.auth': 'Identifiants refusés par la box.',
  'error.unreachable': 'Box injoignable. Êtes-vous sur son Wi-Fi ?',
  'error.pin': 'Le certificat de la box a changé. Si vous avez réinitialisé la box, reconnectez-la depuis Réglages ; sinon, ne continuez pas.',
  'error.busy': 'La box est occupée (quelqu’un est connecté à son interface web ?). Réessayez.',
  'error.generic': 'Erreur : {error}',
  'error.core': 'Le moteur de l’app n’a pas pu être joint. Fermez puis rouvrez Kergui.',
  'error.timeout': 'La box n’a pas répondu à temps. Vérifiez que vous êtes sur son Wi-Fi, puis réessayez.',
} as const;

export type MessageKey = keyof typeof fr;

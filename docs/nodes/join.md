# Rejoindre le PaaS

## Principe

Les agents parlent au server en **gRPC avec TLS mutuel** sur le port 8081 : chacun prouve son identité par un certificat signé par l'autorité de certification (CA) privée de Forgeyard. Rejoindre, c'est obtenir ce certificat.

## La CA de Forgeyard

- Créée au premier démarrage : clé ECDSA P-256, valable 20 ans, dans `data/ca.key` et `data/ca.crt`.
- Son **empreinte** (`sha256:…`) est incluse dans chaque commande d'ajout de node.
- Le certificat serveur (`forgeyard-server`, valable 1 an) est régénéré à chaque démarrage et n'est jamais écrit sur disque. L'agent vérifie toujours le nom `forgeyard-server`, quelle que soit l'adresse qu'il contacte.

## Étapes

1. L'admin crée le node : le server génère un **token** (256 bits, usage unique, 1 h) et n'en garde que le hash.
2. Sur la machine, l'agent télécharge la CA (`GET /api/nodes/ca`) et compare son empreinte à `--ca` **avant** d'envoyer le token. Un faux server ne peut donc pas voler le token.
3. L'agent génère sa clé (ECDSA P-256), qui ne quitte jamais la machine, et envoie une demande de certificat avec le token (`POST /api/nodes/join`).
4. Le server vérifie le token, signe un certificat client `forgeyard-node:<id>` valable 5 ans, note son numéro de série et invalide le token.
5. L'agent enregistre dans son dossier d'état : `node.key`, `node.crt`, `ca.crt`, puis `agent.json` (écrit en dernier : sa présence signifie « a rejoint »).

Les échecs de join sont limités comme les échecs de connexion : 10 par IP en 15 minutes.

À chaque connexion, le server vérifie que le node existe toujours, qu'il est actif et que le numéro de série correspond. C'est ce qui fait office de révocation quand un node est retiré.

## La machine de Forgeyard (node local)

La machine qui fait tourner le server peut être un node sans rien installer : l'agent est déjà dans `docker-compose.yml`.

1. Au démarrage, l'agent local (pas encore rejoint) écrit `/join/host.json` (ports 80/443 libres ou pris, pour le wizard), puis surveille `/join/agent.json` toutes les 3 s.
2. Quand l'admin active la machine (wizard, ou Nodes › « + Ajouter un node » › « Cette machine »), le server crée le node `local` et écrit `/join/agent.json` : adresse du server, token, empreinte de la CA.
3. L'agent rejoint avec, en contactant le server par le réseau Compose (`forgeyard:8080` et `forgeyard:8081`), sans passer par l'adresse publique ni le proxy.
4. Le fichier est supprimé. En cas d'échec (token expiré…), il est renommé `agent.json.failed` et l'admin peut relancer depuis Nodes.

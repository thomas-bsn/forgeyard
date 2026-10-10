# Sécurité

## Secrets chiffrés

- **AES-256-GCM**, avec le nom du réglage comme donnée associée (une valeur chiffrée ne peut pas être recopiée dans un autre réglage).
- Clé : `FORGEYARD_SECRET_KEY` (32 octets en base64), sinon `data/secret.key`, créé au premier démarrage (droits 0600).
- Chiffrés : le Client Secret Discord, les identifiants du fournisseur DNS, les variables d'environnement des apps.
- Jamais renvoyés à l'UI : elle sait seulement qu'une valeur est enregistrée.

Garder la clé hors du volume de données (`FORGEYARD_SECRET_KEY`) protège les secrets d'une simple copie de la base.

## Tokens

Tous les tokens (setup, session, join, lien de secours) font 256 bits aléatoires, et la base ne stocke que leur **hash SHA-256**.

| Token | Durée | Usage |
|---|---|---|
| Setup | jusqu'au redémarrage | une fois |
| Session | 30 jours | |
| Join de node | 1 h | une fois |
| Lien de secours | 15 min | une fois |

## Mots de passe

argon2id (19 MiB, 2 passes), 12 caractères minimum.

## Agents

TLS 1.3 mutuel avec la CA privée de Forgeyard, empreinte de la CA vérifiée avant l'envoi du token de join. Détails dans [nodes/join](../nodes/join.md).

## API

- **CSRF** : toute requête d'écriture doit être en `Content-Type: application/json`, ce qu'un formulaire d'un autre site ne peut pas envoyer sans que le navigateur demande l'autorisation. Les cookies sont en SameSite=Lax.
- Corps JSON limités à 1 Mo, champs inconnus refusés.
- **Limitation** : 10 échecs de connexion ou de join par IP en 15 minutes.
- **Proxies de confiance** : `X-Forwarded-For` / `-Proto` ne sont lus que s'ils viennent d'une IP de `FORGEYARD_TRUSTED_PROXIES` (par défaut `private` : loopback et réseaux privés) ; sinon ils sont effacés. L'IP du client sert à la limitation et à la liste des appareils connectés. Derrière le proxy Cloudflare, ajoutez `cloudflare` (ses plages publiées) : sinon c'est l'IP d'un serveur Cloudflare qui apparaît. Comme Caddy remplace `X-Forwarded-For` quand il ne fait pas confiance à Cloudflare, Forgeyard lit alors l'en-tête `CF-Connecting-IP`, seulement pour une requête venue d'une adresse Cloudflare. Ce n'est pas le défaut, car toute requête passant par Cloudflare pourrait alors choisir son IP.

## Conteneurs des apps

Pas de mode privilégié, `no-new-privileges`, limite mémoire sans swap, pas de montage de l'hôte ni de réseau de l'hôte (l'utilisateur ne choisit que l'image, le port, les variables et la mémoire).

## Limites connues

- Pas d'en-têtes de sécurité HTTP (CSP, HSTS, X-Frame-Options).
- Pas de réseau Docker isolé par utilisateur : toutes les apps d'un node partagent le réseau `forgeyard`.
- Pas d'email ACME configuré pour Let's Encrypt.

# DNS

Code : `internal/dns`, `internal/api/domain.go`, `syncApps` dans `internal/api/apps.go`.

## Modes

Réglés dans le wizard ou dans Réglages › Domaine (superadmin) :

| Mode | Effet |
|---|---|
| `provider` (recommandé) | Forgeyard crée un enregistrement par app chez le fournisseur DNS, avec un token API |
| `wildcard` | Vous créez vous-même `*.domaine.com → IP` une fois ; Forgeyard ne touche pas au DNS |
| `none` | Pas de domaine : les apps sont joignables par IP et port |

`provider` et `wildcard` demandent le domaine et l'**IP publique** vers laquelle pointer (chaque node peut avoir la sienne).

## Fournisseurs

Via [libdns](https://github.com/libdns) :

| Fournisseur | Identifiants |
|---|---|
| Cloudflare | `api_token` (permission Zone › DNS › Edit) |
| OVHcloud | `endpoint`, `application_key`, `application_secret`, `consumer_key` |
| Gandi | `bearer_token` |
| Porkbun | `api_key`, `api_secret_key` |

Le fournisseur est celui qui gère les DNS du domaine (en général là où il a été acheté, sauf si les serveurs DNS ont été déplacés, par exemple vers Cloudflare).

À l'enregistrement, Forgeyard cherche la zone (le domaine, puis ses parents) avec les identifiants : s'ils sont faux, rien n'est enregistré. Les identifiants sont chiffrés et jamais renvoyés à l'UI (un champ secret laissé vide garde l'ancienne valeur).

## Enregistrements

- Un enregistrement **A** (ou AAAA pour une IPv6) par app : `<nom>.<domaine>`, TTL 5 minutes.
- Création de l'app : créé avant le déploiement.
- Changement de l'IP d'un node : les enregistrements de ses apps sont mis à jour.
- Changement des réglages du domaine : toutes les apps sont resynchronisées.
- Suppression de l'app : supprimé.

## Protection de vos autres enregistrements

Forgeyard ne retient (`dns_name` de l'app) que les enregistrements qu'**il** a créés, et ne modifie ou supprime que ceux-là :

- créer une app dont le nom existe déjà dans le DNS (A, AAAA ou CNAME) est refusé ;
- lors d'une resynchronisation, un nom déjà pris par autre chose est laissé intact et signalé.

## Vérification

Réglages › Domaine › Vérifier : en mode `provider`, refait la recherche de zone ; en mode `wildcard`, résout un nom au hasard sous le domaine et compare avec l'IP publique.

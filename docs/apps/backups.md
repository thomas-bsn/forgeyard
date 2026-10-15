# Sauvegardes des volumes

Les volumes nommés des [stacks](stacks.md) se sauvegardent sur un planning vers un **stockage compatible S3**, hors des nodes : un disque qui lâche n'emporte pas ses sauvegardes. Elles sont faites par [restic](https://restic.net) : chiffrées, et dédupliquées d'une fois sur l'autre (seul ce qui change prend de la place).

Code : `internal/api/backups.go` (réglages, planning, restauration), `internal/agent/backup.go`, `internal/docker/run.go`.

## Stockage (admins)

**Réglages › Sauvegardes** : adresse (`https://s3.fr-par.scw.cloud`, `http://minio:9000`…), région si le fournisseur la demande, bucket, dossier (`forgeyard` par défaut), clé d'accès et clé secrète (chiffrée, jamais renvoyée). Le dépôt restic est `s3:<adresse>/<bucket>/<dossier>`, un seul pour toute l'instance.

Le **mot de passe du dépôt** chiffre les sauvegardes. Forgeyard le crée au premier enregistrement (on peut aussi donner le sien, pour reprendre un dépôt existant) ; il est chiffré en base et affiché aux admins : **gardez-en une copie ailleurs**, sans lui les sauvegardes sont illisibles. Le changer ne rend pas lisibles celles déjà faites.

**Tester** ouvre le dépôt depuis un node (la machine de Forgeyard d'abord), et le crée la première fois : le stockage, les clés et le mot de passe sont bons s'il répond.

## Planning (par volume)

Page de la stack › **Volumes et sauvegardes**, pour chaque volume : sauvegardes planifiées oui / non, **tous les jours** à une heure (heure du server) ou **toutes les heures**, et le nombre de sauvegardes **gardées** (les N dernières, 7 par défaut). Il faut que le stockage soit configuré.

- Le server regarde chaque minute ce qui est dû. Un planning créé à 10 h 30 pour 3 h part à 3 h le lendemain ; un server éteint à l'heure prévue rattrape une fois à son retour. Une sauvegarde à la fois sur toute l'instance (le nettoyage de restic verrouille le dépôt).
- **Sauvegarder maintenant** lance une sauvegarde tout de suite.
- La page montre la dernière (réussie ou l'erreur) et la prochaine.

## Comment

L'agent du node de la stack lance restic (`restic/restic:0.17.3`) dans un conteneur éphémère, avec les identifiants du stockage :

- **Fichiers** : le volume est monté en lecture seule et sauvegardé tel quel.
- **Base postgres** : un volume utilisé par un service marqué `x-forgeyard: { backup: { type: postgres } }` est sauvegardé avec `pg_dump -Fc`, lancé par `docker exec` dans le conteneur du service, sa sortie envoyée directement à restic (`--stdin`) : une copie cohérente de la base, là où les fichiers d'une base qui tourne peuvent ne pas l'être. Utilisateur et base : `POSTGRES_USER` (`postgres` sinon) et `POSTGRES_DB` (l'utilisateur sinon) du service. Le service doit être en ligne. Un `pg_dump` qui échoue en route ne laisse pas de sauvegarde.
- Puis `restic forget --keep-last N --prune` ne garde que les N dernières de ce volume.

Les sauvegardes d'un volume portent l'étiquette `stack-<id>-<volume>` : elles restent les siennes si son planning est supprimé puis recréé.

## Échecs

Une sauvegarde qui échoue est notée sur le volume (avec l'erreur), devient un événement du service, et part au salon des admins sur Discord (type **Sauvegardes en échec** dans Réglages › Notifications).

## Restauration

**Restaurer…** liste les sauvegardes du volume (date, taille) ; en choisir une la restaure **dans un volume neuf** du node (`forgeyard-stack-<id>-<volume>-<date>`) : le volume en service n'est jamais écrasé.

- **Fichiers** : restic y remet les fichiers.
- **Postgres** : restic remet le dump dans un volume temporaire, puis un postgres de **la même image** que le service démarre sur le volume neuf (avec les `POSTGRES_USER`, `POSTGRES_DB`, `POSTGRES_PASSWORD` et `PGDATA` du service), et `pg_restore` y charge le dump. Le conteneur s'arrête proprement : le volume est prêt.

Une fois prêt, **Utiliser ce volume** (avec confirmation) le met à la place du volume de la stack : les services qui l'utilisent redémarrent dessus. L'ancien volume reste sur le node (supprimé avec la stack si on le demande alors). Le volume est marqué « restauré » sur la page de la stack.

## Vérifié

Un test d'intégration (`go test -tags integration -run TestBackupRestoreWithMinIO ./internal/agent/`, il lui faut Docker) sauvegarde une base postgres et un volume de fichiers vers un MinIO local, vérifie la rétention, restaure les deux dans des volumes neufs et relit les données. MinIO ne publie plus d'image sur Docker Hub : le test prend `cgr.dev/chainguard/minio` (ou `FORGEYARD_TEST_MINIO_IMAGE`). Le parcours complet par l'API (planning, sauvegarde, restauration, « Utiliser ce volume ») a aussi été vérifié sur une stack réelle.

## Pas encore

Les volumes des apps seules (hors stack), la copie d'un volume vers un autre node, d'autres bases (MySQL, Redis), la suppression des sauvegardes d'une stack supprimée.

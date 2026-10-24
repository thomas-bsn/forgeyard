# Demandes de création avec bind mounts

Conception validée à l'issue de l'entretien et de la confirmation de la synthèse finale. Cette fonctionnalité n'est pas encore implémentée.

## Décisions confirmées

- À la création, cocher l'option d'utilisation des bind mounts transforme la soumission en demande. Une app sans cette option conserve sa création directe.
- Les chemins hôtes concernent la machine qui exécutera l'app, y compris lorsqu'elle est différente du serveur principal Forgeyard.
- La demande conserve la configuration soumise. Aucune app n'est créée ni déployée avant l'approbation.
- Seul le superadmin peut approuver. L'approbation déclenche la création de l'app à partir de la configuration conservée, sans nouvelle saisie par le demandeur.
- Chaque montage demandé indique le chemin source sur l'hôte, la destination dans le conteneur et le mode lecture seule ou lecture/écriture. La lecture seule est le défaut. La demande désigne un node précis.
- Le parcours app couvre Image Docker, Dockerfile et Sandbox ; le parcours projet couvre Docker Compose et l'ensemble de ses services.
- Lorsque le superadmin soumet lui-même une création avec bind mounts, sa soumission vaut approbation et cette approbation est enregistrée ; il peut créer directement.
- L'autorisation porte sur les montages. Le propriétaire et les admins conservent la gestion habituelle de l'app, notamment image, variables, terminal et SSH, avec accès aux fichiers montés dans le mode approuvé.
- Changer une source, une destination, un mode d'accès ou le node exige une nouvelle approbation. L'app conserve sa configuration approuvée pendant l'attente.
- Une demande en attente est figée. Son auteur peut l'annuler ; une modification nécessite une nouvelle demande.
- Le demandeur dispose d'une page « Mes demandes ». La file de traitement est réservée au superadmin. Un refus exige un motif.
- Des notifications Discord sont envoyées si l'intégration est configurée ; les détails techniques et les secrets restent dans Forgeyard.
- Les sources sont des chemins absolus existants sur le node choisi. Aucun dossier manquant n'est créé automatiquement. Le demandeur saisit le chemin ; l'agent le vérifie avant approbation et au déploiement.
- Un node hors ligne empêche l'approbation tant que la vérification des chemins est impossible, y compris pour une soumission du superadmin valant approbation.
- Les montages donnant le contrôle de l'hôte ou de Forgeyard sont interdits, même avec approbation du superadmin : racine `/`, socket Docker, données et secrets internes de Forgeyard. Les liens symboliques sont résolus pour appliquer ces restrictions.
- Lorsque l'option est cochée, un utilisateur ordinaire peut choisir parmi les nodes acceptant ses déploiements. Le formulaire affiche leur nom et leur disponibilité.
- Le nom n'est pas réservé pendant l'attente. Les conditions de création sont revérifiées à l'approbation. Un conflit ou un échec est affiché sur la demande, qui n'est pas marquée comme créée. Seul le superadmin peut relancer la même configuration ; la relance doit éviter les doublons.
- Le superadmin peut révoquer l'autorisation. Les conteneurs concernés sont arrêtés et leur redémarrage avec ces montages est bloqué.
- Supprimer l'app ne supprime jamais les fichiers hôtes. Les sauvegardes Forgeyard excluent les bind mounts ; les données hôtes sont gérées séparément.
- Le superadmin peut consulter toute la configuration nécessaire à sa décision et révéler explicitement les variables secrètes. Les secrets des demandes sont chiffrés comme ceux des apps.
- Le superadmin approuve ou refuse la configuration soumise ; il ne la modifie pas. Un changement nécessite une nouvelle soumission.

## Parcours retenu

1. Le demandeur remplit le formulaire et coche l'option de bind mounts. Il choisit le node et précise les montages.
2. La soumission conserve une configuration figée dans une demande, sans créer l'app ou le projet, ni déployer ses conteneurs. Le demandeur suit sa demande dans « Mes demandes » et peut l'annuler.
3. Le superadmin examine la demande et les chemins vérifiés par l'agent. Il refuse avec un motif ou approuve ; une soumission du superadmin vaut elle-même approbation enregistrée, avec les mêmes validations.
4. L'approbation déclenche la création à partir de la configuration soumise. Les conflits et les échecs restent visibles et ne sont pas assimilés à une création réussie.
5. Une fois créée, l'app reste gérée par son propriétaire et les admins. Un changement des montages ou du node repasse par approbation, avec conservation de la configuration approuvée en attendant. Le superadmin peut révoquer l'autorisation.

## Décision d'architecture

Voir [Portée de l'approbation des bind mounts](../adr/0001-bind-mount-approval-scope.md) et le [glossaire](../../GLOSSARY.md).

## État actuel vérifié

- Les apps sont créées directement par `internal/api/apps.go`. Avec une demande, le Dockerfile et le type Sandbox sont conservés dans la configuration figée ; le Dockerfile n'est construit qu'après approbation. Une Sandbox n'a ni port web ni adresse DNS et garde ses accès SSH/terminal habituels.
- Les apps seules utilisent des volumes Docker nommés ; les projets Compose rejettent les bind mounts dans `internal/compose/compose.go`.
- `internal/api/httputil.go` distingue déjà les admins du superadmin.
- Les admins peuvent actuellement modifier toutes les apps et accéder à leur terminal et à SSH.
- La [documentation de sécurité](../security/README.md) exclut actuellement les montages de l'hôte ; elle devra évoluer lors de l'implémentation.

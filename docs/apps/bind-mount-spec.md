## Problem Statement

Les utilisateurs de Forgeyard ne peuvent pas donner à leurs apps accès à des fichiers déjà présents sur la machine d'exécution. Les apps seules proposent des volumes Docker nommés et les projets Compose refusent les bind mounts. Permettre ces accès exige de distinguer la soumission de la configuration de sa création effective : seul le superadmin doit pouvoir autoriser les montages, sans déléguer ce pouvoir aux admins.

## Solution

Ajouter une option « Utiliser des bind mounts » à la création des apps Image Docker, Dockerfile, Sandbox et des projets Docker Compose. Le demandeur choisit un node et précise les chemins sources, destinations et modes d'accès. La lecture seule est le défaut.

La soumission produit une demande de création avec bind mounts, figée et consultable dans « Mes demandes », sans créer l'app ou le projet ni déployer ses conteneurs. Le superadmin examine la configuration et les chemins vérifiés par l'agent, puis approuve ou refuse avec un motif. L'approbation déclenche la création à partir de la configuration soumise. Quand le superadmin soumet lui-même, sa soumission vaut approbation enregistrée avec les mêmes validations.

Après création, le propriétaire et les admins conservent leurs droits habituels. L'autorisation de bind mounts concerne les accès aux fichiers, sans figer le code exécuté. Changer les montages ou le node nécessite une nouvelle approbation ; le superadmin peut révoquer l'autorisation.

## User Stories

1. En tant que demandeur, je veux créer directement une app sans bind mounts afin de conserver le parcours existant.
2. En tant que demandeur, je veux cocher « Utiliser des bind mounts » afin de demander l'accès à des données hôtes.
3. En tant que demandeur, je veux utiliser ce parcours avec une Image Docker afin d'accéder aux données depuis une image publiée.
4. En tant que demandeur, je veux utiliser ce parcours avec un Dockerfile afin d'accéder aux données depuis une image construite par Forgeyard.
5. En tant que demandeur, je veux utiliser ce parcours avec une Sandbox afin de travailler avec les données autorisées.
6. En tant que demandeur, je veux soumettre un projet Compose avec une seule demande pour tous ses services afin de faire examiner leur configuration ensemble.
7. En tant que demandeur ordinaire, je veux choisir parmi les nodes acceptant mes déploiements afin de désigner la machine où se trouvent les données.
8. En tant que demandeur, je veux voir le nom et la disponibilité des nodes afin de comprendre où l'app s'exécutera et si les chemins sont vérifiables.
9. En tant que demandeur, je veux préciser chaque source hôte par un chemin absolu afin de désigner les données sans ambiguïté.
10. En tant que demandeur, je veux préciser chaque destination dans le conteneur afin que mon app retrouve les données à l'endroit attendu.
11. En tant que demandeur, je veux que la lecture seule soit proposée par défaut afin de limiter les écritures involontaires.
12. En tant que demandeur, je veux demander explicitement la lecture/écriture lorsque nécessaire afin que le superadmin examine ce besoin.
13. En tant que demandeur, je veux soumettre une demande sans créer ni déployer l'app ou le projet afin de respecter l'approbation préalable.
14. En tant que demandeur, je veux retrouver ma configuration soumise afin de ne pas devoir la ressaisir après approbation.
15. En tant que demandeur, je veux suivre mes demandes dans « Mes demandes » afin de connaître leur résultat.
16. En tant que demandeur, je veux annuler une demande en attente afin de retirer une configuration que je ne souhaite plus déployer.
17. En tant que demandeur, je veux soumettre une nouvelle demande lorsque ma configuration change afin que la décision porte sur une version figée.
18. En tant que demandeur, je veux connaître le motif d'un refus afin de pouvoir corriger ma prochaine soumission.
19. En tant que superadmin, je veux une file de traitement qui m'est réservée afin de retrouver les demandes à examiner.
20. En tant que superadmin, je veux consulter toute la configuration soumise afin de comprendre les accès demandés et leur utilisation.
21. En tant que superadmin, je veux révéler explicitement les variables secrètes lorsque nécessaire afin de prendre une décision éclairée.
22. En tant que superadmin, je veux connaître le résultat de la vérification des sources par l'agent afin d'approuver des chemins existants sur le bon node.
23. En tant que superadmin, je veux approuver une demande afin de déclencher sa création sans nouvelle saisie par son auteur.
24. En tant que superadmin, je veux refuser une demande en donnant un motif obligatoire afin d'expliquer ma décision.
25. En tant que superadmin, je veux que la configuration examinée reste inchangée pendant ma décision afin d'approuver exactement ce qui a été soumis.
26. En tant que superadmin créant une app, je veux que ma soumission vaille approbation enregistrée afin d'éviter une seconde validation de ma propre demande.
27. En tant que superadmin, je veux que les mêmes validations s'appliquent à mes créations afin de respecter les restrictions des montages.
28. En tant que responsable d'instance, je veux que les admins ordinaires ne puissent ni approuver ni refuser une demande afin de réserver ce pouvoir au superadmin.
29. En tant que responsable d'instance, je veux interdire les montages donnant le contrôle de l'hôte ou de Forgeyard afin de préserver les limites de cette autorisation.
30. En tant que responsable d'instance, je veux que les liens symboliques soient résolus lors des vérifications afin qu'ils ne contournent pas les restrictions.
31. En tant que demandeur, je veux qu'un chemin manquant soit signalé sans création automatique de dossier afin de repérer une erreur de source.
32. En tant que superadmin, je veux qu'un node hors ligne empêche l'approbation tant que les chemins sont invérifiables afin de ne pas autoriser un accès non vérifié.
33. En tant que propriétaire d'app, je veux que les chemins soient vérifiés à nouveau au déploiement afin qu'une source disparue ne soit pas remplacée par un dossier créé automatiquement.
34. En tant que demandeur, je veux qu'une demande en attente ne réserve pas le nom afin de ne pas bloquer inutilement les autres créations.
35. En tant que superadmin, je veux que les conditions de création soient revérifiées à l'approbation afin de détecter les conflits apparus pendant l'attente.
36. En tant que demandeur, je veux voir un conflit ou un échec de création sur ma demande afin de distinguer l'approbation d'une création réussie.
37. En tant que superadmin, je veux relancer la même configuration après un échec sans créer de doublons afin de reprendre la création de façon fiable.
38. En tant que propriétaire ou admin, je veux conserver la gestion habituelle de l'image et des variables après création afin de faire évoluer l'app sans réapprobation du code.
39. En tant que propriétaire ou admin, je veux conserver le terminal et SSH afin d'utiliser les fichiers montés dans le mode autorisé.
40. En tant que propriétaire, je veux demander un changement des sources, destinations, modes d'accès ou du node afin de faire évoluer les montages avec une nouvelle approbation.
41. En tant que propriétaire, je veux conserver la configuration approuvée pendant l'examen d'un changement afin de ne pas appliquer des accès non autorisés à l'app existante.
42. En tant que superadmin, je veux révoquer une autorisation afin d'arrêter les conteneurs concernés et de bloquer leur redémarrage avec ces montages.
43. En tant que propriétaire des données hôtes, je veux que la suppression d'une app conserve mes fichiers afin de ne pas perdre des données gérées séparément.
44. En tant que propriétaire, je veux que les sauvegardes Forgeyard excluent les bind mounts afin de distinguer les volumes gérés par Forgeyard des données hôtes.
45. En tant que demandeur ou superadmin, je veux recevoir les notifications de suivi sur Discord lorsque l'intégration est configurée afin de suivre les demandes.
46. En tant que demandeur, je veux que les secrets soient chiffrés et que les notifications Discord ne contiennent ni secrets ni détails techniques afin de garder ces informations dans Forgeyard.

## Implementation Decisions

- Étendre les parcours de création et de modification des apps et projets, le stockage, la coordination server-agent et la réconciliation des conteneurs. Le server continue de ne pas communiquer directement avec Docker : les vérifications des chemins et les montages sont réalisés par l'agent du node concerné.
- Utiliser le vocabulaire du domaine : « demande de création avec bind mounts » pour la soumission précédant la création, « autorisation de bind mounts » pour l'accord portant sur les accès. Une demande en attente n'est pas une app en attente.
- Conserver le parcours direct existant pour les créations sans l'option. Avec l'option, distinguer explicitement dans l'interface et les réponses de l'API une demande soumise d'une app ou d'un projet créé.
- Faire porter chaque demande sur une configuration figée, son auteur, le node choisi et l'ensemble des montages demandés. Pour Compose, une seule demande couvre tous les services du projet ; l'examen présente les montages de chacun.
- Faire évoluer le stockage pour conserver les demandes, leurs configurations, leurs décisions, les motifs de refus, les résultats de création et les autorisations révocables. Les secrets conservés dans les demandes utilisent le chiffrement existant des secrets des apps.
- Chaque montage précise la source absolue existante sur l'hôte, sa destination dans le conteneur et le mode lecture seule ou lecture/écriture. La lecture seule est le défaut. Ne pas créer automatiquement une source manquante.
- L'option permet aux utilisateurs ordinaires de choisir parmi les nodes acceptant leurs déploiements, avec nom et disponibilité affichés. Les chemins sont ceux de la machine d'exécution, qui peut être différente du serveur principal Forgeyard.
- Faire vérifier les chemins par l'agent avant approbation et au déploiement. Une approbation est impossible si le node est hors ligne et que les chemins ne peuvent pas être vérifiés. Cette règle s'applique également au superadmin soumettant lui-même une création.
- Interdire, même pour le superadmin, les montages donnant le contrôle de l'hôte ou de Forgeyard, notamment la racine `/`, le socket Docker et les données et secrets internes de Forgeyard. Résoudre les liens symboliques pour appliquer les restrictions aux chemins réels ; vérifier qu'un montage de répertoire englobant ne contourne pas ces restrictions.
- Réserver à l'auteur la consultation de ses demandes et l'annulation de ses demandes en attente ; réserver au superadmin la file de traitement, l'approbation, le refus et la relance après échec. Contrôler ces permissions côté API, y compris lorsqu'un admin ordinaire appelle directement les opérations.
- Le superadmin examine la configuration entière et peut révéler explicitement ses variables secrètes. Il peut approuver ou refuser avec un motif obligatoire, sans modifier la configuration soumise. Une modification exige une nouvelle soumission.
- La soumission d'une création par le superadmin vaut approbation enregistrée, sans seconde étape manuelle et avec les mêmes vérifications.
- L'approbation déclenche la création à partir de la configuration conservée. Avant cette étape, ne pas créer l'app ou le projet, ni leurs conteneurs ou leur DNS. Le nom n'est pas réservé pendant l'attente ; revérifier les conditions de création à l'approbation.
- Distinguer attente, annulation, refus, approbation et résultat de création dans les comportements observables. Une erreur ne doit pas être présentée comme une création réussie. Une relance réservée au superadmin reprend la même configuration et ne crée pas de doublons, y compris si la première tentative a partiellement abouti.
- Conserver les droits habituels du propriétaire et des admins après création : image, variables, terminal et SSH. L'approbation autorise des accès précis aux fichiers, sans approuver chaque version du code exécuté. Ce compromis fait l'objet d'une ADR acceptée.
- Toute modification des sources, destinations, modes d'accès ou du node exige une nouvelle approbation. Pendant l'attente, conserver la configuration déjà approuvée et empêcher l'application des nouveaux montages par les autres parcours de modification.
- Permettre au superadmin de révoquer une autorisation : arrêter les conteneurs concernés et empêcher leur redémarrage avec ces montages. Appliquer ce blocage lors de la réconciliation, afin qu'un redémarrage de l'agent ne rétablisse pas l'accès révoqué.
- Ne jamais supprimer les fichiers hôtes lors de la suppression d'une app. Exclure les bind mounts des sauvegardes Forgeyard ; leur protection est gérée séparément.
- Réutiliser l'intégration Discord configurée pour les notifications de suivi. Garder les détails techniques et les secrets dans Forgeyard.
- Les noms exacts des tables, les routes supplémentaires et les structures internes restent des choix d'implémentation ; préserver les contrats et limites d'autorisation ci-dessus.

## Testing Decisions

- Points d'entrée validés par l'utilisateur : couverture principale par l'API HTTP existante avec une base temporaire et un agent simulé sur la vraie connexion gRPC ; complément par des tests d'intégration Docker côté agent pour les effets réels sur les montages.
- Tester les comportements observables, les permissions et les effets sur les ressources et fichiers. Éviter les tests qui recopient les fonctions internes, imposent le découpage des modules ou n'observent que les détails du schéma.
- Réutiliser les dispositifs existants des tests de cycle de vie des apps et projets : serveur HTTP de test, sessions pour les différents rôles, base temporaire, fournisseur DNS en mémoire et agent simulé connecté par gRPC avec TLS mutuel. Le parcours des demandes de comptes et les tests de notifications fournissent également des précédents.
- Couvrir via l'API la soumission sans création, les quatre sources de création, la demande unique d'un projet Compose, l'immuabilité, l'annulation, le refus motivé, l'approbation, la création par le superadmin, la consultation des demandes et la révélation des secrets.
- Tester avec utilisateur ordinaire, autre utilisateur, admin et superadmin : interdire les décisions et relances aux rôles non autorisés, empêcher la consultation des demandes d'autrui et vérifier l'absence de contournement par les API de création ou modification existantes.
- Vérifier l'absence d'app, de projet, de DNS et d'état de déploiement créé avant approbation. Contrôler après approbation les ressources et l'état voulu envoyé au node, pour la configuration exacte et les modes d'accès soumis.
- Couvrir les sources manquantes, les chemins relatifs, les chemins sensibles, les liens symboliques, les répertoires englobant une source interdite et les nodes hors ligne. Vérifier que les vérifications sont faites sur le node désigné et répétées au déploiement.
- Tester les conflits de nom entre soumission et approbation, les tentatives de décision répétées ou concurrentes, les erreurs de création et les relances, afin de vérifier l'absence de doublons et la restitution d'un résultat fidèle.
- Tester la conservation des accès habituels après création, la nécessité d'une nouvelle approbation en cas de changement de montages ou de node, le maintien de l'ancienne configuration pendant l'attente et le blocage des montages révoqués.
- Vérifier le chiffrement des secrets avec les mécanismes existants, leur absence dans les notifications et leur révélation limitée au parcours explicite autorisé. Tester le suivi avec et sans intégration Discord configurée.
- Compléter par des tests d'intégration de l'agent avec Docker et des fichiers temporaires : lecture possible, écriture refusée en lecture seule, écriture effective en lecture/écriture, refus sans création de dossier si la source manque, arrêt après révocation, absence de redémarrage automatique avec l'accès révoqué, et conservation des fichiers après suppression de l'app.
- Les tests d'intégration Docker et les tests de sauvegarde/restauration déjà présents constituent le précédent pour vérifier les effets réels de l'agent. Vérifier également que les bind mounts ne sont pas inclus dans les sauvegardes Forgeyard.
- Vérifier dans l'interface la case, les champs des montages, le choix du node, le bouton de soumission, « Mes demandes », la file superadmin et les retours d'erreur ; conserver la couverture automatisée principale aux points d'entrée validés, sans imposer un nouvel outil de tests navigateur.

## Out of Scope

- Faire approuver les créations sans bind mounts.
- Déléguer l'approbation, le refus ou la révocation aux admins ordinaires.
- Donner une permission générale de monter librement n'importe quel chemin de n'importe quel node.
- Autoriser le superadmin à contourner les restrictions sur les montages donnant le contrôle de l'hôte ou de Forgeyard.
- Figer ou approuver chaque changement d'image, de variables ou de code après création.
- Modifier en place une demande en attente ou faire réécrire la configuration soumise par le superadmin.
- Réserver un nom pendant l'attente, créer automatiquement un dossier hôte manquant ou transférer des fichiers entre nodes.
- Sauvegarder, restaurer ou supprimer les données hôtes via le cycle de vie des apps Forgeyard.
- Modifier les règles existantes concernant le mode privilégié et le réseau de l'hôte.

## Further Notes

La conception détaillée, le glossaire et l'ADR sur la portée de l'approbation ont été rédigés dans le dépôt et validés au cours de la conversation. La synthèse finale a été acceptée ; les points d'entrée des tests ont ensuite été explicitement validés. Cette spécification est prête pour implémentation et doit porter le label `ready-for-agent`.

La documentation actuelle exclut les montages de l'hôte : l'implémentation devra mettre à jour les descriptions de création, des projets Compose, des sauvegardes et de la sécurité pour refléter les autorisations précises et leurs limites.

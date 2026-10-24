# Forgeyard

Forgeyard héberge des apps sur des machines appelées nodes.

## Langage

**Demande de création avec bind mounts** :
Soumission d'une configuration d'app ou de projet nécessitant l'approbation du superadmin avant sa création. Elle précise les chemins hôtes, leurs destinations, les modes d'accès et le node concerné.
_Éviter_ : app en attente (l'app n'existe pas encore).

**Autorisation de bind mounts** :
Accord du superadmin permettant à une app ou à un projet d'utiliser des chemins hôtes précis sur un node donné, aux destinations et dans les modes d'accès approuvés. Cet accord est révocable.
_Éviter_ : accès libre à l'hôte, approbation du code.

**Superadmin** :
Rôle d'administration de l'instance, seul habilité à approuver une demande de création avec bind mounts.
_Éviter_ : admin (rôle distinct).

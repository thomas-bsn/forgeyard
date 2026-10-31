---
status: accepted
---

# Approuver les montages tout en conservant la gestion habituelle des apps

Forgeyard interdit actuellement les montages de l'hôte. Pour permettre l'accès à des données hôtes, la conception retenue réserve au superadmin l'approbation de montages précis, liés à un node, une source, une destination et un mode d'accès, avant la création ou leur modification.

L'approbation porte sur ces accès et ne fige pas le code exécuté : le propriétaire et les admins conservent la gestion habituelle de l'image, des variables, du terminal et de SSH. Ce choix préserve leur autonomie de gestion, avec pour conséquence qu'ils peuvent utiliser les fichiers montés dans le mode autorisé sans nouvelle approbation à chaque changement de code. Les montages donnant le contrôle de l'hôte ou de Forgeyard restent interdits ; le superadmin peut révoquer l'autorisation, arrêter les conteneurs concernés et empêcher leur redémarrage avec ces montages.

La révocation est également accessible aux admins. Une nouvelle demande explicite peut réautoriser les accès sur le même node après validation par un superadmin ; une demande antérieure à la révocation ne rétablit pas ces accès.

Ces décisions et la synthèse finale ont été confirmées pendant l'entretien. L'implémentation reste à venir ; le parcours détaillé figure dans la [conception](../apps/bind-mount-requests.md).

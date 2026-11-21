package msg

// Textos generales de la interfaz. Los de cada módulo (tipos de check,
// temas…) se registran en su propio fichero.
//
// Convención: los textos que se usan con Msg (errores de validación y
// mensajes de resultado) formatean sus argumentos con %v.
func init() {
	Register(map[string]Text{
		// Generales
		"app.name":    {"Qaphi", "Qaphi", "Qaphi", "Qaphi"},
		"app.tagline": {"Monitor de servicios", "Service monitor", "Dienstmonitor", "Moniteur de services"},

		// Navegación
		"nav.home":     {"Estado", "Status", "Status", "État"},
		"nav.admin":    {"Checks", "Checks", "Checks", "Checks"},
		"nav.settings": {"Ajustes", "Settings", "Einstellungen", "Paramètres"},
		"nav.logout":   {"Cerrar sesión", "Log out", "Abmelden", "Se déconnecter"},

		// Configuración inicial
		"setup.title": {"Configuración inicial", "Initial setup", "Ersteinrichtung", "Configuration initiale"},
		"setup.intro": {
			"Elige la contraseña del usuario admin. Para comprobar que tienes acceso al equipo, introduce también el token de configuración que Qaphi escribe en su log al arrancar.",
			"Choose the password for the admin user. To prove that you have access to this machine, also enter the setup token that Qaphi writes to its log on startup.",
			"Wählen Sie das Passwort für den Benutzer admin. Um zu bestätigen, dass Sie Zugriff auf das Gerät haben, geben Sie außerdem das Einrichtungstoken ein, das Qaphi beim Start in sein Log schreibt.",
			"Choisissez le mot de passe de l’utilisateur admin. Pour prouver que vous avez accès à la machine, saisissez aussi le jeton de configuration que Qaphi écrit dans son journal au démarrage.",
		},
		"setup.language": {"Idioma", "Language", "Sprache", "Langue"},
		"setup.token":    {"Token de configuración", "Setup token", "Einrichtungstoken", "Jeton de configuration"},
		"setup.token.help": {
			"En una instalación con systemd: journalctl -u qaphi | grep token",
			"On a systemd installation: journalctl -u qaphi | grep token",
			"Bei einer systemd-Installation: journalctl -u qaphi | grep token",
			"Avec une installation systemd : journalctl -u qaphi | grep token",
		},
		"setup.password": {"Contraseña", "Password", "Passwort", "Mot de passe"},
		"setup.confirm":  {"Repite la contraseña", "Repeat the password", "Passwort wiederholen", "Répétez le mot de passe"},
		"setup.submit":   {"Guardar y entrar", "Save and sign in", "Speichern und anmelden", "Enregistrer et se connecter"},
		"setup.err.token": {
			"El token de configuración no es correcto.",
			"The setup token is not correct.",
			"Das Einrichtungstoken ist nicht korrekt.",
			"Le jeton de configuration est incorrect.",
		},

		// Inicio de sesión
		"login.title":    {"Iniciar sesión", "Sign in", "Anmelden", "Connexion"},
		"login.user":     {"Usuario", "User", "Benutzer", "Utilisateur"},
		"login.password": {"Contraseña", "Password", "Passwort", "Mot de passe"},
		"login.submit":   {"Entrar", "Sign in", "Anmelden", "Se connecter"},
		"login.err":      {"Contraseña incorrecta.", "Wrong password.", "Falsches Passwort.", "Mot de passe incorrect."},
		"login.locked": {
			"Demasiados intentos fallidos. Vuelve a intentarlo en %s.",
			"Too many failed attempts. Try again in %s.",
			"Zu viele Fehlversuche. Versuchen Sie es in %s erneut.",
			"Trop de tentatives échouées. Réessayez dans %s.",
		},

		// Contraseñas
		"auth.err.password_short": {
			"La contraseña debe tener al menos 10 caracteres.",
			"The password must be at least 10 characters long.",
			"Das Passwort muss mindestens 10 Zeichen lang sein.",
			"Le mot de passe doit comporter au moins 10 caractères.",
		},
		"auth.err.password_long": {
			"La contraseña no puede superar los 72 bytes.",
			"The password cannot exceed 72 bytes.",
			"Das Passwort darf 72 Bytes nicht überschreiten.",
			"Le mot de passe ne peut pas dépasser 72 octets.",
		},
		"auth.err.password_mismatch": {
			"Las contraseñas no coinciden.",
			"The passwords do not match.",
			"Die Passwörter stimmen nicht überein.",
			"Les mots de passe ne correspondent pas.",
		},

		// Página de estado
		"home.title":       {"Estado de los servicios", "Service status", "Dienststatus", "État des services"},
		"home.empty":       {"Todavía no hay checks configurados.", "No checks configured yet.", "Noch keine Checks eingerichtet.", "Aucun check configuré pour l’instant."},
		"home.empty.admin": {"Créalos en Checks.", "Create them under Checks.", "Legen Sie sie unter Checks an.", "Créez-les dans Checks."},
		"home.live":        {"Se actualiza automáticamente.", "Updates automatically.", "Wird automatisch aktualisiert.", "Mise à jour automatique."},

		// Resumen global: «3/4 (1 caído)»
		"summary.format":    {"%d/%d (%s)", "%d/%d (%s)", "%d/%d (%s)", "%d/%d (%s)"},
		"summary.all_up":    {"todos up", "all up", "alle in Betrieb", "tous opérationnels"},
		"summary.down.one":  {"%d caído", "%d down", "%d ausgefallen", "%d en panne"},
		"summary.down.many": {"%d caídos", "%d down", "%d ausgefallen", "%d en panne"},
		"summary.unknown":   {"%d sin datos", "%d no data", "%d ohne Daten", "%d sans données"},
		"summary.none":      {"0/0 (sin checks activos)", "0/0 (no active checks)", "0/0 (keine aktiven Checks)", "0/0 (aucun check actif)"},

		// Columnas
		"col.name":     {"Nombre", "Name", "Name", "Nom"},
		"col.group":    {"Grupo", "Group", "Gruppe", "Groupe"},
		"col.type":     {"Tipo", "Type", "Typ", "Type"},
		"col.target":   {"Destino", "Target", "Ziel", "Cible"},
		"col.status":   {"Estado", "Status", "Status", "État"},
		"col.latency":  {"Respuesta", "Response", "Antwortzeit", "Réponse"},
		"col.last":     {"Última comprobación", "Last check", "Letzte Prüfung", "Dernière vérification"},
		"col.detail":   {"Detalle", "Details", "Details", "Détail"},
		"col.interval": {"Intervalo", "Interval", "Intervall", "Intervalle"},
		"col.actions":  {"Acciones", "Actions", "Aktionen", "Actions"},

		// Estados
		"status.up":      {"Operativo", "Up", "In Betrieb", "Opérationnel"},
		"status.down":    {"Caído", "Down", "Ausgefallen", "En panne"},
		"status.unknown": {"Sin datos", "No data", "Keine Daten", "Pas de données"},
		"status.paused":  {"Pausado", "Paused", "Pausiert", "En pause"},

		// Tiempo relativo
		"time.never": {"nunca", "never", "nie", "jamais"},
		"time.now":   {"ahora", "now", "jetzt", "maintenant"},
		"time.ago.s": {"hace %d s", "%d s ago", "vor %d s", "il y a %d s"},
		"time.ago.m": {"hace %d min", "%d min ago", "vor %d min", "il y a %d min"},
		"time.ago.h": {"hace %d h", "%d h ago", "vor %d h", "il y a %d h"},
		"time.ago.d": {"hace %d d", "%d d ago", "vor %d T", "il y a %d j"},

		// Checks (administración)
		"admin.title":       {"Checks", "Checks", "Checks", "Checks"},
		"admin.new":         {"Nuevo check", "New check", "Neuer Check", "Nouveau check"},
		"admin.edit":        {"Editar", "Edit", "Bearbeiten", "Modifier"},
		"admin.delete":      {"Borrar", "Delete", "Löschen", "Supprimer"},
		"admin.empty":       {"No hay checks todavía.", "No checks yet.", "Noch keine Checks.", "Pas encore de checks."},
		"admin.choose_type": {"Elige el tipo de check", "Choose the check type", "Check-Typ wählen", "Choisissez le type de check"},
		"check.new.title":   {"Nuevo check · %s", "New check · %s", "Neuer Check · %s", "Nouveau check · %s"},
		"check.edit.title":  {"Editar check · %s", "Edit check · %s", "Check bearbeiten · %s", "Modifier le check · %s"},
		"check.f.common":    {"Datos generales", "General", "Allgemein", "Général"},
		"check.f.params":    {"Parámetros", "Parameters", "Parameter", "Paramètres"},
		"check.f.name":      {"Nombre", "Name", "Name", "Nom"},
		"check.f.group":     {"Grupo", "Group", "Gruppe", "Groupe"},
		"check.f.group.help": {
			"Opcional, p. ej. Red, Servicios o Externos.",
			"Optional, e.g. Network, Services or External.",
			"Optional, z. B. Netzwerk, Dienste oder Extern.",
			"Facultatif, p. ex. Réseau, Services ou Externes.",
		},
		"check.f.interval": {"Intervalo", "Interval", "Intervall", "Intervalle"},
		"check.f.interval.help": {
			"Cada cuánto se comprueba (entre 10s y 24h).",
			"How often it is checked (between 10s and 24h).",
			"Wie oft geprüft wird (zwischen 10s und 24h).",
			"Fréquence de vérification (entre 10s et 24h).",
		},
		"check.f.paused": {"Pausado (no se comprueba)", "Paused (not checked)", "Pausiert (wird nicht geprüft)", "En pause (non vérifié)"},
		"check.err.interval_timeout": {
			"El intervalo debe ser mayor que el tiempo máximo del check.",
			"The interval must be longer than the check timeout.",
			"Das Intervall muss länger als das Zeitlimit des Checks sein.",
			"L’intervalle doit être supérieur au délai maximal du check.",
		},
		"delete.title": {"Borrar check", "Delete check", "Check löschen", "Supprimer le check"},
		"delete.confirm": {
			"¿Seguro que quieres borrar «%s»? También se borrará su historial.",
			"Are you sure you want to delete “%s”? Its history will be deleted too.",
			"Möchten Sie „%s“ wirklich löschen? Der Verlauf wird ebenfalls gelöscht.",
			"Voulez-vous vraiment supprimer « %s » ? Son historique sera également supprimé.",
		},
		"delete.submit": {"Borrar definitivamente", "Delete permanently", "Endgültig löschen", "Supprimer définitivement"},

		// Formularios
		"form.save":   {"Guardar", "Save", "Speichern", "Enregistrer"},
		"form.cancel": {"Cancelar", "Cancel", "Abbrechen", "Annuler"},
		"form.errors": {"Revisa los campos marcados.", "Please check the highlighted fields.", "Bitte prüfen Sie die markierten Felder.", "Vérifiez les champs signalés."},
		"form.secret.keep": {
			"Déjalo vacío para conservar el valor actual.",
			"Leave empty to keep the current value.",
			"Leer lassen, um den aktuellen Wert zu behalten.",
			"Laissez vide pour conserver la valeur actuelle.",
		},

		// Mensajes de confirmación
		"flash.created": {
			"Check creado. Se comprobará en unos segundos.",
			"Check created. It will be checked in a few seconds.",
			"Check angelegt. Er wird in wenigen Sekunden geprüft.",
			"Check créé. Il sera vérifié dans quelques secondes.",
		},
		"flash.updated": {"Check guardado.", "Check saved.", "Check gespeichert.", "Check enregistré."},
		"flash.deleted": {"Check borrado.", "Check deleted.", "Check gelöscht.", "Check supprimé."},
		"flash.welcome": {
			"Contraseña guardada. Ya puedes crear tus checks.",
			"Password saved. You can now create your checks.",
			"Passwort gespeichert. Sie können jetzt Ihre Checks anlegen.",
			"Mot de passe enregistré. Vous pouvez maintenant créer vos checks.",
		},
		"flash.settings":  {"Ajustes guardados.", "Settings saved.", "Einstellungen gespeichert.", "Paramètres enregistrés."},
		"flash.network":   {"Configuración de red confirmada.", "Network settings confirmed.", "Netzwerkeinstellungen bestätigt.", "Configuration réseau confirmée."},
		"flash.cancelled": {"Cambio de red cancelado.", "Network change cancelled.", "Netzwerkänderung abgebrochen.", "Modification réseau annulée."},
		"flash.reverted": {
			"El cambio de red no se confirmó a tiempo y se restauró la configuración anterior.",
			"The network change was not confirmed in time and the previous settings were restored.",
			"Die Netzwerkänderung wurde nicht rechtzeitig bestätigt; die vorherigen Einstellungen wurden wiederhergestellt.",
			"La modification réseau n’a pas été confirmée à temps ; la configuration précédente a été rétablie.",
		},
		"flash.cert": {"Certificado regenerado.", "Certificate regenerated.", "Zertifikat neu erzeugt.", "Certificat régénéré."},

		// Errores HTTP
		"http.notfound": {"Página no encontrada.", "Page not found.", "Seite nicht gefunden.", "Page introuvable."},
		"http.forbidden": {
			"La sesión del formulario no es válida o ha caducado. Vuelve a cargar la página e inténtalo de nuevo.",
			"The form session is invalid or has expired. Reload the page and try again.",
			"Die Formularsitzung ist ungültig oder abgelaufen. Laden Sie die Seite neu und versuchen Sie es erneut.",
			"La session du formulaire n’est pas valide ou a expiré. Rechargez la page et réessayez.",
		},
		"http.toolarge": {"El formulario es demasiado grande.", "The form is too large.", "Das Formular ist zu groß.", "Le formulaire est trop volumineux."},
		"http.error":    {"Error", "Error", "Fehler", "Erreur"},
		"http.internal": {"Error interno del servidor.", "Internal server error.", "Interner Serverfehler.", "Erreur interne du serveur."},

		// Validación de campos (se usan con Msg: argumentos con %v)
		"field.required": {"Este campo es obligatorio.", "This field is required.", "Dieses Feld ist erforderlich.", "Ce champ est obligatoire."},
		"field.too_long": {"Admite como máximo %v caracteres.", "At most %v characters.", "Höchstens %v Zeichen.", "%v caractères au maximum."},
		"field.int":      {"Debe ser un número entero.", "Must be a whole number.", "Muss eine ganze Zahl sein.", "Doit être un nombre entier."},
		"field.min":      {"Debe ser como mínimo %v.", "Must be at least %v.", "Muss mindestens %v sein.", "Doit être au moins %v."},
		"field.max":      {"Debe ser como máximo %v.", "Must be at most %v.", "Darf höchstens %v sein.", "Doit être au plus %v."},
		"field.duration": {
			"Duración no válida (ejemplos: 30s, 5m, 1h).",
			"Invalid duration (examples: 30s, 5m, 1h).",
			"Ungültige Dauer (Beispiele: 30s, 5m, 1h).",
			"Durée non valide (exemples : 30s, 5m, 1h).",
		},
		"field.bool":   {"Valor no válido.", "Invalid value.", "Ungültiger Wert.", "Valeur non valide."},
		"field.select": {"Opción no válida.", "Invalid option.", "Ungültige Option.", "Option non valide."},

		// Ajustes generales
		"settings.title":    {"Ajustes", "Settings", "Einstellungen", "Paramètres"},
		"settings.general":  {"General", "General", "Allgemein", "Général"},
		"settings.language": {"Idioma", "Language", "Sprache", "Langue"},
		"settings.theme":    {"Tema", "Theme", "Design", "Thème"},
		"settings.timezone": {"Zona horaria", "Time zone", "Zeitzone", "Fuseau horaire"},
		"settings.timezone.system": {
			"La del sistema (%s)", "System (%s)", "System (%s)", "Celui du système (%s)",
		},

		// Red
		"settings.network":        {"Red", "Network", "Netzwerk", "Réseau"},
		"settings.net.current":    {"Dirección actual: %s", "Current address: %s", "Aktuelle Adresse: %s", "Adresse actuelle : %s"},
		"settings.net.bind":       {"Dirección de escucha", "Listen address", "Lauschadresse", "Adresse d’écoute"},
		"settings.net.bind.all":   {"Todas las interfaces", "All interfaces", "Alle Schnittstellen", "Toutes les interfaces"},
		"settings.net.bind.local": {"Solo este equipo (127.0.0.1)", "This machine only (127.0.0.1)", "Nur dieses Gerät (127.0.0.1)", "Cette machine uniquement (127.0.0.1)"},
		"settings.net.bind.ip":    {"Solo la IP %s", "Only IP %s", "Nur IP %s", "Uniquement l’IP %s"},
		"settings.net.port":       {"Puerto", "Port", "Port", "Port"},
		"settings.net.port.help":  {"Entre 1024 y 65535. Por defecto, 9077.", "Between 1024 and 65535. Default: 9077.", "Zwischen 1024 und 65535. Standard: 9077.", "Entre 1024 et 65535. Par défaut : 9077."},
		"settings.net.protocol":   {"Protocolo", "Protocol", "Protokoll", "Protocole"},
		"settings.net.http":       {"HTTP", "HTTP", "HTTP", "HTTP"},
		"settings.net.https":      {"HTTPS (certificado autofirmado)", "HTTPS (self-signed certificate)", "HTTPS (selbstsigniertes Zertifikat)", "HTTPS (certificat autosigné)"},
		"settings.net.https.help": {
			"El navegador mostrará un aviso la primera vez; acepta la excepción para continuar.",
			"Your browser will show a warning the first time; accept the exception to continue.",
			"Ihr Browser zeigt beim ersten Mal eine Warnung an; akzeptieren Sie die Ausnahme, um fortzufahren.",
			"Votre navigateur affichera un avertissement la première fois ; acceptez l’exception pour continuer.",
		},
		"settings.net.confirm.help": {
			"Tras aplicar, tendrás 2 minutos para abrir la nueva dirección y confirmarla; si no, se restaurará la actual.",
			"After applying, you will have 2 minutes to open the new address and confirm it; otherwise the current one will be restored.",
			"Nach dem Anwenden haben Sie 2 Minuten Zeit, die neue Adresse zu öffnen und zu bestätigen; andernfalls wird die aktuelle wiederhergestellt.",
			"Après application, vous aurez 2 minutes pour ouvrir la nouvelle adresse et la confirmer ; sinon, l’adresse actuelle sera rétablie.",
		},
		"settings.net.apply": {"Aplicar cambios de red", "Apply network changes", "Netzwerkänderungen anwenden", "Appliquer les modifications réseau"},
		"settings.net.locked": {
			"La dirección está fijada al arrancar (--listen o QAPHI_LISTEN) y no se puede cambiar desde aquí.",
			"The address is fixed at startup (--listen or QAPHI_LISTEN) and cannot be changed here.",
			"Die Adresse ist beim Start festgelegt (--listen oder QAPHI_LISTEN) und kann hier nicht geändert werden.",
			"L’adresse est fixée au démarrage (--listen ou QAPHI_LISTEN) et ne peut pas être modifiée ici.",
		},
		"settings.net.unchanged": {"No hay cambios en la red.", "There are no network changes.", "Keine Netzwerkänderungen.", "Aucune modification réseau."},
		"settings.net.err.port": {
			"El puerto debe estar entre 1024 y 65535.",
			"The port must be between 1024 and 65535.",
			"Der Port muss zwischen 1024 und 65535 liegen.",
			"Le port doit être compris entre 1024 et 65535.",
		},
		"settings.net.err.bind": {
			"Esa dirección no pertenece a este equipo.",
			"That address does not belong to this machine.",
			"Diese Adresse gehört nicht zu diesem Gerät.",
			"Cette adresse n’appartient pas à cette machine.",
		},
		"settings.net.err.inuse": {
			"No se puede usar %s: el puerto ya está en uso.",
			"Cannot use %s: the port is already in use.",
			"%s kann nicht verwendet werden: Der Port wird bereits verwendet.",
			"Impossible d’utiliser %s : le port est déjà utilisé.",
		},
		"settings.net.err.open": {
			"No se puede usar %s: %s",
			"Cannot use %s: %s",
			"%s kann nicht verwendet werden: %s",
			"Impossible d’utiliser %s : %s",
		},
		"settings.net.err.pending": {
			"Ya hay un cambio de red pendiente de confirmar.",
			"A network change is already pending confirmation.",
			"Eine Netzwerkänderung wartet bereits auf Bestätigung.",
			"Une modification réseau est déjà en attente de confirmation.",
		},
		"settings.net.pending": {"Cambio pendiente de confirmar", "Change pending confirmation", "Änderung wartet auf Bestätigung", "Modification en attente de confirmation"},
		"settings.net.pending.text": {
			"La nueva configuración ya está activa. Ábrela y confírmala antes de las %s; si no, se restaurará la anterior.",
			"The new configuration is already active. Open it and confirm it before %s; otherwise the previous one will be restored.",
			"Die neue Konfiguration ist bereits aktiv. Öffnen und bestätigen Sie sie vor %s; andernfalls wird die vorherige wiederhergestellt.",
			"La nouvelle configuration est déjà active. Ouvrez-la et confirmez-la avant %s ; sinon, la précédente sera rétablie.",
		},
		"settings.net.pending.inplace": {
			"Al usar el mismo puerto, la dirección anterior deja de responder en cuanto se aplica el cambio.",
			"Because the port is the same, the previous address stops responding as soon as the change is applied.",
			"Da der Port gleich bleibt, antwortet die vorherige Adresse nicht mehr, sobald die Änderung angewendet wird.",
			"Le port étant le même, l’ancienne adresse cesse de répondre dès que la modification est appliquée.",
		},
		"settings.net.pending.open":   {"Abrir la nueva dirección", "Open the new address", "Neue Adresse öffnen", "Ouvrir la nouvelle adresse"},
		"settings.net.pending.cancel": {"Cancelar el cambio", "Cancel the change", "Änderung abbrechen", "Annuler la modification"},
		"settings.net.confirm.title":  {"Confirmar la configuración de red", "Confirm network settings", "Netzwerkeinstellungen bestätigen", "Confirmer la configuration réseau"},
		"settings.net.confirm.text": {
			"Has llegado a Qaphi por la nueva dirección. Confírmala para conservarla.",
			"You reached Qaphi through the new address. Confirm it to keep it.",
			"Sie haben Qaphi über die neue Adresse erreicht. Bestätigen Sie sie, um sie beizubehalten.",
			"Vous avez accédé à Qaphi par la nouvelle adresse. Confirmez-la pour la conserver.",
		},
		"settings.net.confirm.submit": {"Confirmar", "Confirm", "Bestätigen", "Confirmer"},
		"settings.net.confirm.wrong": {
			"Esta confirmación debe hacerse desde la nueva dirección: %s",
			"This confirmation must be made from the new address: %s",
			"Diese Bestätigung muss über die neue Adresse erfolgen: %s",
			"Cette confirmation doit être faite depuis la nouvelle adresse : %s",
		},
		"settings.net.confirm.none": {
			"No hay ningún cambio de red pendiente.",
			"There is no pending network change.",
			"Es gibt keine ausstehende Netzwerkänderung.",
			"Aucune modification réseau en attente.",
		},

		// Certificado
		"settings.cert": {"Certificado HTTPS", "HTTPS certificate", "HTTPS-Zertifikat", "Certificat HTTPS"},
		"settings.cert.info": {
			"Autofirmado, válido hasta el %s. Huella SHA-256: %s",
			"Self-signed, valid until %s. SHA-256 fingerprint: %s",
			"Selbstsigniert, gültig bis %s. SHA-256-Fingerabdruck: %s",
			"Autosigné, valable jusqu’au %s. Empreinte SHA-256 : %s",
		},
		"settings.cert.none": {
			"Se generará al activar HTTPS.",
			"It will be generated when HTTPS is enabled.",
			"Es wird beim Aktivieren von HTTPS erzeugt.",
			"Il sera généré lors de l’activation de HTTPS.",
		},
		"settings.cert.regen": {"Regenerar certificado", "Regenerate certificate", "Zertifikat neu erzeugen", "Régénérer le certificat"},
		"settings.cert.regen.help": {
			"Útil si cambian el nombre o las IP del equipo. El navegador volverá a pedir la excepción.",
			"Useful if the machine’s name or IP addresses change. The browser will ask for the exception again.",
			"Nützlich, wenn sich Name oder IP-Adressen des Geräts ändern. Der Browser fragt erneut nach der Ausnahme.",
			"Utile si le nom ou les adresses IP de la machine changent. Le navigateur demandera de nouveau l’exception.",
		},
	})
}

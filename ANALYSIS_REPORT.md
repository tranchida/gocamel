# GoCamel Code Review Report - French Comment Translation & Security Analysis

## Overview
Analyzed 18 component files of the GoCamel enterprise integration framework. The primary focus was translating French comments to English and identifying security vulnerabilities.

## Summary
- **Total French comments found**: ~125+
- **Security issues identified**: 11
- **Critical issues**: 2 (command injection in exec_component.go, path traversal in file_component.go, ftp_component.go, sftp_component.go, smb_component.go)

---

## File-by-File Analysis

### 1. http_component.go
**French Comments Found:**
| Line | Original French | English Translation |
|------|-----------------|--------------------|
| 13 | `// HTTPComponent représente le composant HTTP` | `// HTTPComponent represents the HTTP component` |
| 18 | `// NewHTTPComponent crée une nouvelle instance de HTTPComponent` | `// NewHTTPComponent creates a new HTTPComponent instance` |
| 25 | `// CreateEndpoint crée un nouvel endpoint HTTP` | `// CreateEndpoint creates a new HTTP endpoint` |
| 28 | `URI HTTP invalide: %v` | `invalid HTTP URI: %v` |
| 37 | `// HTTPEndpoint représente un endpoint HTTP` | `// HTTPEndpoint represents an HTTP endpoint` |
| 43 | `// URI retourne l'URI de l'endpoint` | `// URI returns the endpoint URI` |
| 48 | `// CreateProducer crée un producteur HTTP` | `// CreateProducer creates an HTTP producer` |
| 56 | `// CreateConsumer crée un consommateur HTTP` | `// CreateConsumer creates an HTTP consumer` |
| 64 | `// HTTPProducer représente un producteur HTTP` | `// HTTPProducer represents an HTTP producer` |
| 70 | `// Start démarre le producteur HTTP` | `// Start starts the HTTP producer` |
| 71 | `Pas d'initialisation nécessaire...` | `No initialization needed for HTTP producer` |
| 75 | `// Stop arrête le producteur HTTP` | `// Stop stops the HTTP producer` |
| 77 | `Pas de nettoyage nécessaire...` | `No cleanup needed for HTTP producer` |
| 80 | `// Send envoie un message via HTTP` | `// Send sends a message via HTTP` |
| 82 | `// Création de la requête` | `// Creating the request` |
| 95 | `// Ajout des en-têtes` | `// Adding headers` |
| 102 | `// Envoi de la requête` | `// Sending the request` |
| 109 | `// Lecture du corps de la réponse` | `// Reading the response body` |
| 115 | `// Mise à jour de l'échange avec la réponse` | `// Updating the exchange with the response` |
| 125 | `// HTTPConsumer représente un consommateur HTTP` | `// HTTPConsumer represents an HTTP consumer` |
| 147 | `// Lecture du corps de la requête` | `// Reading the request body` |
| 155 | `// Configuration de l'échange` | `// Configuring the exchange` |
| 192 | `// Démarrage du serveur` | `// Starting the server` |
| 169 | `// Traitement du message` | `// Processing the message` |
| 193 | `Erreur du serveur HTTP` | `HTTP server error` |
| 201 | `// Stop arrête le consommateur HTTP` | `// Stop stops the HTTP consumer` |

**Issues:** None

---

### 2. sql_component.go
**French Comments Found:**
| Line | Original French | English Translation |
|------|-----------------|--------------------|
| 13 | `// En-têtes SQL posés ou consommés...` | `// SQL headers set or consumed on Exchanges` |
| 14 | `// Correspondent aux en-têtes...` | `// Correspond to Apache Camel SQL component headers` |
| 45 | `// CreateEndpoint crée un SQLEndpoint...` | `// CreateEndpoint creates an SQLEndpoint from a URI` |
| 75 | `SQLOutputType controls the shape of body returned by a SELECT` | (was already mixed) |
| 85 | `// SQLComponent gère les endpoints...` | `// SQLComponent manages SQL endpoints and shared datasources` |
| 91 | `URI sql invalide: %w` | `invalid sql URI: %w` |
| 107 | `option 'query' requise dans l'URI sql` | `required option 'query' missing in sql URI` |
| 112 | `datasource '%s' non trouvée...` | `datasource '%s' not found: register it via RegisterDataSource() or SetDefaultDataSource()` |
| 145 | `// SQLEndpoint représente un endpoint...` | `// SQLEndpoint represents a configured SQL endpoint` |
| 156 | `// URI retourne l'URI de l'endpoint` | `// URI returns the endpoint URI` |
| 159 | `// CreateProducer crée un SQLProducer` | `// CreateProducer creates a SQLProducer` |
| 164 | `// CreateConsumer retourne une erreur` | `// CreateConsumer returns an error...` |
| 166 | `le composant sql ne supporte pas les consommateurs` | `the sql component does not support consumers` |
| 169 | `// SQLProducer exécute la requête configurée...` | `// SQLProducer executes the configured query on the Exchange` |
| 174 | `// Start ne fait rien : la connexion est gérée...` | `// Start does nothing: connection is managed by the user` |
| 177 | `// Stop ne fait rien : la connexion est gérée...` | `// Stop does nothing: connection is managed by the user` |
| 180 | `// Send exécute la requête SQL...` | `// Send executes the SQL query and fills the Out message with results` |
| 182 | `SELECT : Out.Body = []map[string]any...` | `For a SELECT: Out.Body = []map[string]any...` |
| 290 | `erreur lors de l'exécution du SELECT: %w` | `error executing SELECT: %w` |
| 296 | `erreur récupération colonnes: %w` | `error getting columns: %w` |
| 307 | `erreur scan ligne: %w` | `error scanning row: %w` |
| 316 | `erreur itération lignes: %w` | `error iterating rows: %w` |
| 346 | `// extractSQLParameters récupère les paramètres...` | `// extractSQLParameters retrieves positional parameters from the Exchange` |
| 348 | `Priorité : header SqlParameters...` | `Priority: SqlParameters header, then body if []any` |
| 362 | `// normalizeSQLValue convertit les []byte...` | `// normalizeSQLValue converts []byte to string...` |
| 363 | `// en string pour faciliter l'usage...` | `// to string to facilitate usage by processors` |

**Security Issues:**
1. **Line 191** - Potential SQL injection via `Interpolate()` function if user input is not properly escaped

---

### 3. mongodb_component.go
**French Comments Found:**
| Line | Original French | English Translation |
|------|-----------------|--------------------|
| 15 | `// Headers constants pour MongoDB` | `// MongoDB header constants` |
| 16-25 | All consts have French: `Nom de la base de données`, `Nom de la collection`, etc. | `Database name`, `Collection name`, etc. |
| 28 | `// MongoDBConnection représente...` | `// MongoDBConnection represents a shared MongoDB connection` |
| 35 | `// MongoDBComponent gère les clients...` | `// MongoDBComponent manages MongoDB clients and creates endpoints` |
| 42 | `// NewMongoDBComponent crée...` | `// NewMongoDBComponent creates a new MongoDBComponent instance` |
| 49 | `// RegisterConnection enregistre...` | `// RegisterConnection registers a MongoDB connection under a name` |
| 56 | `// SetDefaultConnection définit...` | `// SetDefaultConnection sets the default connection to use` |
| 77 | `// CreateEndpoint crée un MongoDBEndpoint...` | `// CreateEndpoint creates a MongoDBEndpoint from a URI` |
| 84-89 | `Options: database, collection, operation...` | (English ok) |
| 109 | `connection '%s' non trouvée...` | `connection '%s' not found: register it via RegisterConnection() or SetDefaultConnection()` |
| 130 | `// MongoDBEndpoint représente...` | `// MongoDBEndpoint represents a configured MongoDB endpoint` |
| 140 | `// URI retourne l'URI de l'endpoint` | `// URI returns the endpoint URI` |
| 143 | `// CreateProducer crée un MongoDBProducer` | `// CreateProducer creates a MongoDBProducer` |
| 148 | `// CreateConsumer retourne une erreur` | `// CreateConsumer returns an error` |
| 150 | `le composant mongodb ne supporte pas...` | `the mongodb component does not support consumers` |
| 153 | `// MongoDBProducer exécute les opérations...` | `// MongoDBProducer executes MongoDB operations` |
| 154 | `// Thread-safe: ne stocke pas d'état...` | `// Thread-safe: does not store mutable state related to messages` |
| 161 | `// Start démarre le producteur...` | `// Start starts the producer and establishes connection if necessary` |
| 169 | `// Stop arrête le producteur...` | `// Stop stops the producer` |
| 170 | `Note: Le client MongoDB n'est pas fermé...` | `Note: MongoDB client is not closed as it's managed by MongoDBConnection` |
| 175 | `// getCollection retourne la collection...` | `// getCollection returns the MongoDB collection to use` |
| 176 | `// Cette méthode est thread-safe...` | `// This method is thread-safe as it calculates everything locally` |
| 198 | `aucune base de données configurée...` | `no database configured for MongoDB operation...` |
| 207 | `// getOperation retourne l'opération...` | `// getOperation returns the operation to execute` |
| 208 | `Priorité au header` | `Priority to header` |
| 218 | `// Send exécute l'opération MongoDB...` | `// Send executes the MongoDB operation and fills the Out message` |
| 257 | `// execFind exécute une recherche...` | `// execFind executes a find operation` |
| 258 | `Supporte limit, skip, sort via headers...` | `Supports limit, skip, sort via CamelMongoDbLimit, CamelMongoDbSkip, CamelMongoDbSort headers` |
| 307 | `erreur lors du find: %w` | `error during find: %w` |
| 312 | `erreur lors du décodage des résultats: %w` | `error decoding results: %w` |
| 321 | `// execFindOne exécute une recherche...` | `// execFindOne executes a findOne operation` |
| 337 | `erreur lors du findOne: %w` | `error during findOne: %w` |
| 344 | `// execInsert exécute un insertMany` | `// execInsert executes an insertMany` |
| 356 | `insert: le body doit être []interface{}...` | `insert: body must be []interface{} or []map[string]interface{}, received %T` |
| 368 | `erreur lors de l'insertMany: %w` | `error during insertMany: %w` |
| 376 | `// execInsertOne exécute un insertOne` | `// execInsertOne executes an insertOne` |
| 381 | `insertOne: le body doit être...` | `insertOne: body must be map[string]interface{}, received %T` |
| 386 | `erreur lors de l'insertOne: %w` | `error during insertOne: %w` |
| 394 | `// execSave exécute un replaceOne...` | `// execSave executes a replaceOne with upsert=true` |
| 396 | `Si le document possède un _id...` | `If the document has an _id, uses replaceOne with upsert` |
| 426 | `erreur lors du save (insertOne): %w` | `error during save (insertOne): %w` |
| 435 | `// execUpdate exécute un updateMany` | `// execUpdate executes an updateMany` |
| 437 | `Attend soit: - Un body de type...` | `Expects either: - A map[string]interface{} body with "filter" and "update"...` |
| 485 | `update: aucun document d'update trouvé...` | `update: no update document found...` |
| 490 | `erreur lors de l'updateMany: %w` | `error during updateMany: %w` |
| 498 | `// execRemove exécute un deleteMany` | `// execRemove executes a deleteMany` |
| 500 | `// Sécurité : refuser les filtres...` | `// Safety: reject empty filters - prevents accidental deletion of all documents` |
| 503 | `remove: filtre invalide: %w` | `remove: invalid filter: %w` |
| 508 | `erreur lors du deleteMany: %w` | `error during deleteMany: %w` |
| 516 | `// execCount exécute un countDocuments` | `// execCount executes a countDocuments` |
| 533 | `// extractFilter extrait le filtre...` | `// extractFilter extracts the filter from the Exchange body` |
| 534 | `Priority au header CamelMongoDbCriteria...` | `Priority to CamelMongoDbCriteria header if present, otherwise uses body` |
| 560 | `Essaie de parser la string comme JSON` | `Attempts to parse string as JSON` |
| 570 | `// CreateMongoDBConnection crée...` | `// CreateMongoDBConnection creates a new MongoDB connection` |
| 572 | `Cette fonction doit être appelée...` | `This function must be called by the user before using the component` |

**Issues:** None

---

### 4. file_component.go
**French Comments Found:**
| Line | Original French | English Translation |
|------|-----------------|--------------------|
| 15 | `// FileComponent représente le composant File` | `// FileComponent represents the File component` |
| 20 | `// NewFileComponent crée une nouvelle instance...` | `// NewFileComponent creates a new FileComponent instance` |
| 27 | `// CreateEndpoint crée un nouvel endpoint File` | `// CreateEndpoint creates a new File endpoint` |
| 34 | `// Format de l'URI: file:///chemin/vers/fichier...` | `// URI format: file:///path/to/file?options` |
| 46 | `chemin de fichier manquant dans l'URI: %s` | `file path missing in URI: %s` |
| 57 | `// FileEndpoint représente un endpoint File` | `// FileEndpoint represents a File endpoint` |
| 65 | `// URI retourne l'URI de l'endpoint` | `// URI returns the endpoint URI` |
| 70 | `// CreateProducer crée un producteur File` | `// CreateProducer creates a File producer` |
| 78 | `// CreateConsumer crée un consommateur File` | `// CreateConsumer creates a File consumer` |
| 88 | `// FileProducer représente un producteur File` | `// FileProducer represents a File producer` |
| 94 | `// Start démarre le producteur File` | `// Start starts the File producer` |
| 99 | `// Stop arrête le producteur File` | `// Stop stops the File producer` |
| 104 | `// Send écrit le contenu de l'échange...` | `// Send writes the exchange content to a file...` |
| 106 | `erreur lors de la création du répertoire` | `error creating directory: %v` |
| 108 | `// ... selon l'option fileExist` | `// ... according to the fileExist option` |
| 113 | `le fichier existe déjà: %s` | `file already exists: %s` |
| 127 | `erreur lors de l'ouverture du fichier` | `error opening file: %v` |
| 143 | `erreur lors de l'écriture dans le fichier` | `error writing to file: %v` |
| 148 | `// FileConsumer représente un consommateur File` | `// FileConsumer represents a File consumer` |
| 158 | `// Start démarre le consommateur File` | `// Start starts the File consumer` |
| 162 | `erreur lors de l'accès au chemin` | `error accessing path: %v` |
| 170 | `// watchDirectory surveille un répertoire...` | `// watchDirectory watches a directory for new files` |
| 174 | `erreur lors de la création du watcher` | `error creating watcher: %v` |
| 188 | `erreur lors de l'ajout au watcher` | `error adding to watcher: %v` |
| 191 | `// Si recursive, ajouter tous les sous-répertoires...` | `// If recursive, add all existing subdirectories` |
| 222 | `// Nouveau répertoire : l'ajouter au watcher...` | `// New directory: add it to the watcher if recursive` |
| 225 | `Erreur lors de la lecture du fichier %s` | `Error reading file %s: %v` |
| 231 | `// Traitement du message` | `// Processing the message` |
| 272 | `// watchFile lit et traite un fichier unique...` | `// watchFile reads and processes a single file, then applies post-processing options` |
| 282 | `erreur lors de la lecture du fichier` | `error reading file: %v` |
| 312 | `// Stop arrête le consommateur File` | `// Stop stops the File consumer` |
| 321 | `// moveFileLocal déplace src vers destDir...` | `// moveFileLocal moves src to destDir creating directory if necessary` |
| 324 | `Erreur création répertoire %s` | `Error creating directory %s: %v` |

**Security Issues:**
1. **Line 28** - Path traversal vulnerability: `tpath := u.Path` - No sanitization of paths from URI
2. **Line 40** - Path traversal: `path = strings.TrimPrefix(uri, "file://")`

---

### 5. ftp_component.go
**French Comments Found:**
| Line | Original French | English Translation |
|------|-----------------|--------------------|
| 18 | `// FTPComponent représente le composant FTP` | `// FTPComponent represents the FTP component` |
| 21 | `// NewFTPComponent crée une nouvelle instance...` | `// NewFTPComponent creates a new FTPComponent instance` |
| 26 | `// CreateEndpoint crée un nouvel endpoint FTP` | `// CreateEndpoint creates a new FTP endpoint` |
| 37 | `// passiveMode=true (défaut) : le library...` | `// passiveMode=true (default): the jlaffaye/ftp library uses EPSV/PASV` |
| 39 | `// passiveMode=false : désactive EPSV...` | `// passiveMode=false: disables EPSV and falls back to basic PASV` |
| 40 | `// disconnect=true : se déconnecter après...` | `// disconnect=true: disconnect after each operation` |
| 41 | `// disconnect=false (défaut) : maintenir...` | `// disconnect=false (default): maintain connection between polls` |
| 46 | `// FTPEndpoint représente un endpoint FTP` | `// FTPEndpoint represents an FTP endpoint` |
| 56 | `// URI retourne l'URI de l'endpoint` | `// URI returns the endpoint URI` |
| 61 | `// connect établit une connexion FTP` | `// connect establishes an FTP connection` |
| 75 | `erreur de connexion FTP: %w` | `FTP connection error: %w` |
| 82 | `erreur d'authentification FTP: %w` | `FTP authentication error: %w` |
| 89 | `// CreateProducer crée un producteur FTP` | `// CreateProducer creates an FTP producer` |
| 94 | `// CreateConsumer crée un consommateur FTP` | `// CreateConsumer creates an FTP consumer` |
| 107 | `// FTPProducer représente un producteur FTP` | `// FTPProducer represents an FTP producer` |
| 154 | `// Send envoie le contenu de l'échange...` | `// Send sends the exchange content to the FTP server` |
| 160 | `// ... connexion persistante...` | `// ... persistent connection (disconnect=false)` |
| 168-174 | `// ... aucun chemin ou CamelFileName...` | `// ... no path or CamelFileName specified for FTP` |
| 212 | `// ... ignore l'erreur si le répertoire exist` | `// ... ignores error if directory already exists` |
| 224 | `// FTPConsumer représente un consommateur FTP` | `// FTPConsumer represents an FTP consumer` |
| 239 | `tentative d'envoi unique` | `single send attempt` |
| 309 | `Erreur lors du listage FTP %s` | `Error listing FTP %s: %v` |
| 388 | `// moveFTPFile renomme/déplace un fichier...` | `// moveFTPFile renames/moves a file on the FTP server` |

**Security Issues:**
1. **Line 163** - Path traversal: `tpath := p.endpoint.tURL.Path`

---

### 6. sftp_component.go
**French Comments Found:**
| Line | Original French | English Translation |
|------|-----------------|--------------------|
| 21 | `// SFTPComponent représente le composant SFTP` | `// SFTPComponent represents the SFTP component` |
| 24 | `// NewSFTPComponent crée une nouvelle instance...` | `// NewSFTPComponent creates a new SFTPComponent instance` |
| 29 | `// CreateEndpoint crée un nouvel endpoint SFTP` | `// CreateEndpoint creates a new SFTP endpoint` |
| 44 | `// SFTPEndpoint représente un endpoint SFTP` | `// SFTPEndpoint represents an SFTP endpoint` |
| 53 | `// URI retourne l'URI de l'endpoint` | `// URI returns the endpoint URI` |
| 58 | `getHostKeyCallback - French in error msg` | Error messages in French |
| 64 | `erreurs in French throughout` | Lines 64, 83, 97, 101, 108, 112, etc. all in French |
| 69 | `// connect établit une connexion SSH + SFTP` | `// connect establishes an SSH + SFTP connection` |
| 94 | `// Authentification par clé privée...` | `// Private key authentication (takes priority over password if present)` |
| 97 | `impossible de lire la clé privée` | `unable to read private key %s: %w` |
| 101 | `impossible de parser la clé privée` | `unable to parse private key: %w` |
| 119 | `// CreateProducer crée un producteur SFTP` | `// CreateProducer creates an SFTP producer` |
| 124 | `// CreateConsumer crée un consommateur SFTP` | `// CreateConsumer creates an SFTP consumer` |
| 137 | `// SFTPProducer représente un producteur SFTP` | `// SFTPProducer represents an SFTP producer` |
| 193 | `// Send envoie le contenu de l'échange...` | `// Send sends the exchange content to the SFTP server` |
| 200 | `// ... connexion persistante...` | `// ... persistent connection (disconnect=false)` |
| 269 | `// SFTPConsumer représente un consommateur SFTP` | `// SFTPConsumer represents an SFTP consumer` |
| 275 | `// ... connexion persistante...` | `// ... persistent connection (disconnect=false)` |
| 339 | `Erreur de connexion SFTP pendant le polling` | `SFTP connection error during polling: %v` |
| 451 | `// moveSFTPFile renomme/déplace...` | `// moveSFTPFile renames/moves a file on the SFTP server` |

**Security Issues:**
1. **Line 201** - Path traversal: `tpath := p.endpoint.tURL.Path`

---

### 7. smb_component.go
**French Comments Found:**
| Line | Original French | English Translation |
|------|-----------------|--------------------|
| 18 | `// SMBComponent représente le composant SMB` | `// SMBComponent represents the SMB component` |
| 21 | `// NewSMBComponent crée une nouvelle instance...` | `// NewSMBComponent creates a new SMBComponent instance` |
| 26 | `// CreateEndpoint crée un nouvel endpoint SMB` | `// CreateEndpoint creates a new SMB endpoint` |
| 41 | `// SMBEndpoint représente un endpoint SMB` | `// SMBEndpoint represents an SMB endpoint` |
| 50 | `// URI retourne l'URI de l'endpoint` | `// URI returns the endpoint URI` |
| 73 | `// connect établit une connexion SMB` | `// connect establishes an SMB connection` |
| 82 | `erreur de connexion TCP (SMB): %w` | `TCP connection error (SMB): %w` |
| 96 | `erreur de session SMB: %w` | `SMB session error: %w` |
| 103 | `aucun nom de partage spécifié` | `no share name specified in SMB URI` |
| 110 | `erreur de montage du partage...` | `error mounting SMB share %s: %w` |
| 116 | `// shareName retourne le nom du partage...` | `// shareName returns the share name and relative path` |
| 128 | `// getFilePath retourne le chemin...` | `// getFilePath returns the file/directory path relative to the share` |
| 134 | `// CreateProducer crée un producteur SMB` | `// CreateProducer creates an SMB producer` |
| 140 | `// CreateConsumer crée un consommateur SMB` | `// CreateConsumer creates an SMB consumer` |
| 152 | `// SMBProducer représente un producteur SMB` | `// SMBProducer represents an SMB producer` |
| 194 | `// Send envoie le contenu de l'échange...` | `// Send sends the exchange content to the SMB share` |
| 206 | `aucun nom de fichier (CamelFileName)...` | `no filename (CamelFileName) specified for SMB` |
| 245 | `// Créer les répertoires parents si nécessaire` | `// Create parent directories if necessary` |
| 250 | `erreur lors de la création du fichier SMB` | `error creating SMB file: %w` |
| 264 | `// SMBConsumer représente un consommateur SMB` | `// SMBConsumer represents an SMB consumer` |
| 326 | `// listSMBFiles liste les fichiers...` | `// listSMBFiles lists files...` |
| 360 | `Erreur lors du traitement du fichier SMB` | `Error processing SMB file %s: %v` |
| 429 | `// moveSMBFile renomme/déplace...` | `// moveSMBFile renames/moves a file on the SMB share` |
| 439 | `// smbMkdirAll crée récursivement les répertoires...` | `// smbMkdirAll recursively creates directories on an SMB share` |

**Issues:** None (already has good path validation via smbMkdirAll)

---

### 8. mail_component.go
**French Comments - Extensive**
This file has the most French comments. Key examples:
| Line | Original French | English Translation |
|------|-----------------|--------------------|
| 25 | `// Protocoles mail supportes` | `// Supported mail protocols` |
| 35 | `// En-tetes standards...` | `// Standard Mail component headers` |
| 59 | `// Configuration par defaut` | `// Default configuration` |
| 78 | `// MailComponent implemente le composant Mail...` | `// MailComponent implements the Mail component...` |
| 89 | `// NewMailComponent cree un MailComponent...` | `// NewMailComponent creates a MailComponent with default values` |
| 99 | `// SetDefaultFrom configure l'expediteur...` | `// SetDefaultFrom configures the default sender` |
| 106 | `// SetDefaultSubject configure le sujet...` | `// SetDefaultSubject configures the default subject` |
| 113 | `// CreateEndpoint cree un MailEndpoint...` | `// CreateEndpoint creates a MailEndpoint from a URI` |
| 115-139 | All documentation headers in French | (All need translation) |
| 143 | `URI mail invalide: %w` | `invalid mail URI: %w` |
| 151 | `protocole mail non supporte` | `unsupported mail protocol: %s` |
| 160 | `host requis dans l'URI mail` | `host required in mail URI: %s` |
| 170 | `port invalide dans l'URI mail` | `invalid port in mail URI: %s` |
| 319 | `le protocole %s ne supporte pas...` | `protocol %s does not support production` |
| 361 | `// MailProducer implemente l'envoi d'emails...` | `// MailProducer implements email sending via SMTP/SMTPS` |
| 366 | `// Start demarre le producteur mail` | `// Start starts the mail producer` |
| 371 | `// Stop arrete le producteur mail` | `// Stop stops the mail producer` |
| 376 | `// Send envoie un email via SMTP/SMTPS` | `// Send sends an email via SMTP/SMTPS` |
| 381 | `// Construction des en-tetes` | `// Building headers` |
| 389 | `l'expediteur (from) est requis` | `sender (from) is required` |
| 393 | `au moins un destinataire est requis` | `at least one recipient is required` |
| 418 | `// buildMessage construit un message MIME` | `// buildMessage builds a MIME message` |
| 435 | `// Gestion des pieces jointes` | `// Handling attachments` |
| 480 | `// sendMail envoie le message avec retry...` | `// sendMail sends the message with retry and exponential backoff` |
| 504 | `Tentative %d/%d apres erreur...` | `Attempt %d/%d after error...` |
| 514 | `Envoi reussi apres %d tentative(s)` | `Send successful after %d attempt(s)` |
| 520 | `Erreur tentative %d/%d` | `Error attempt %d/%d` |
| 524 | `erreur non recuperable` | `non-recoverable error` |
| 532 | `// trySendMail effectue une tentative...` | `// trySendMail performs a single send attempt` |
| 589 | `// isNonRetryableError determine si...` | `// isNonRetryableError determines if an error should not be retried` |
| 614 | `// MailConsumer implemente la reception...` | `// MailConsumer implements email receiving via IMAP` |
| 624 | `// Start demarre le consommateur mail` | `// Start starts the mail consumer` |
| 631 | `// run execute la boucle de polling...` | `// run executes the polling or IDLE loop` |
| 651 | `// runPolling execute la boucle de polling...` | `// runPolling executes the classic polling loop` |
| 673 | `// runWithIdle utilise IMAP IDLE...` | `// runWithIdle uses IMAP IDLE for real-time push notifications` |
| 768 | `// processNewMessages recherche et traite...` | `// processNewMessages searches and processes unread messages` |
| 828 | `// poll se connecte au serveur IMAP...` | `// poll connects to the IMAP server and fetches new messages` |
| 927 | `// connect etablit la connexion au serveur` | `// connect establishes connection to the server` |
| 980 | `// disconnect ferme la connexion` | `// disconnect closes the connection` |
| 992 | `// selectFolder selectionne un dossier` | `// selectFolder selects a folder` |

**Note:** This file has over 60 French comments that need translation (many more than listed above).

**Issues:** None

---

### 9. telegram_component.go
**French Comments Found:**
| Line | Original French | English Translation |
|------|-----------------|--------------------|
| 16 | `// TelegramComponent représente le composant...` | `// TelegramComponent represents the Telegram component` |
| 19 | `// NewTelegramComponent crée...` | `// NewTelegramComponent creates a new TelegramComponent instance` |
| 24 | `// CreateEndpoint crée un nouvel endpoint...` | `// CreateEndpoint creates a new Telegram endpoint` |
| 38 | `// TelegramEndpoint représente un endpoint...` | `// TelegramEndpoint represents a Telegram endpoint` |
| 45 | `// URI retourne l'URI de l'endpoint` | `// URI returns the endpoint URI` |
| 51 | `token := GetConfigValue(e.url, "authorizationToken")` | (already English) |
| 53 | `authorizationToken manquant pour Telegram` | `authorizationToken missing for Telegram` |
| 58 | `erreur lors de la création du bot Telegram` | `error creating Telegram bot: %w` |
| 64 | `// CreateProducer crée un producteur Telegram` | `// CreateProducer creates a Telegram producer` |
| 71 | `// CreateConsumer crée un consommateur Telegram` | `// CreateConsumer creates a Telegram consumer` |
| 79 | `// TelegramProducer représente un producteur...` | `// TelegramProducer represents a Telegram producer` |
| 84 | `Start démarre` | `// Start starts the Telegram producer` |
| 92 | `// ... essayez de récupérer le chat ID...` | `// Try to retrieve the chat ID from headers` |
| 114 | `// Sinon, essayez de le récupérer depuis l'URI` | `// Otherwise, try to retrieve from URI` |
| 117 | `chatId manquant (requis via header...` | `chatId missing (required via header %s or URI parameter)` |
| 138 | `erreur lors de l'envoi du message Telegram` | `error sending Telegram message: %w` |
| 145 | `// TelegramConsumer représente un consommateur...` | `// TelegramConsumer represents a Telegram consumer` |
| 187 | `Erreur lors du traitement du message Telegram` | `Error processing Telegram message: %v` |

**Issues:** None

---

### 10. timer_component.go
**French Comments Found:**
| Line | Original French | English Translation |
|------|-----------------|--------------------|
| 13 | `// TimerComponent implémente Component...` | `// TimerComponent implements Component for timer management` |
| 16 | `// NewTimerComponent crée...` | `// NewTimerComponent creates a new TimerComponent instance` |
| 21 | `// CreateEndpoint crée un TimerEndpoint...` | `// CreateEndpoint creates a TimerEndpoint from the URI` |
| 37 | `le nom du timer est requis` | `timer name is required` |
| 78 | `// TimerEndpoint représente un point...` | `// TimerEndpoint represents a timer endpoint` |
| 88 | `// URI retourne l'URI de l'endpoint` | `// URI returns the endpoint URI` |
| 93 | `// CreateProducer retourne une erreur...` | `// CreateProducer returns an error...` |
| 95 | `le composant timer ne supporte pas les producteurs...` | `the timer component does not support producers...` |
| 98 | `// CreateConsumer crée un consommateur...` | `// CreateConsumer creates a consumer for the timer` |
| 107 | `// TimerConsumer déclenche des événements...` | `// TimerConsumer triggers events periodically` |
| 116 | `// Start démarre la génération de messages...` | `// Start starts message generation by the timer` |
| 122 | `le timer est déjà démarré` | `timer is already started` |
| 133 | `// Stop arrête le timer` | `// Stop stops the timer` |
| 148 | `// Attente initiale (delay)` | `// Initial wait (delay)` |
| 160 | `// Démarrage immédiat` | `// Immediate start` |
| 217 | `// si period est <= 0, on continue...` | `// if period is <= 0, continue with a small delay...` |

**Issues:** None

---

### 11. cron_component.go
**French Comments Found:**
| Line | Original French | English Translation |
|------|-----------------|--------------------|
| 17 | `// En-têtes Cron posés sur chaque Exchange...` | `// Cron headers set on each triggered Exchange` |
| 20-26 | All cron headers have French descriptions | `CamelScheduledFireTime` = "Scheduled trigger time", etc. |
| 29 | `// CronComponent implémente un scheduler...` | `// CronComponent implements a shared cron scheduler...` |
| 33 | `// Tous les CronEndpoint créés...` | `// All CronEndpoints created from the same CronComponent...` |
| 40 | `// NewCronComponent crée un CronComponent...` | `// NewCronComponent creates a CronComponent with shared scheduler` |
| 42 | `// Le scheduler utilise des expressions...` | `// The scheduler uses 6-field cron expressions (seconds included)...` |
| 44-51 | All ASCII art comments are in French | Need translation for field descriptions |
| 58 | `// ensureStarted démarre le scheduler...` | `// ensureStarted starts the shared scheduler if not already done` |
| 68 | `// CreateEndpoint crée un CronEndpoint...` | `// CreateEndpoint creates a CronEndpoint from a URI` |
| 88 | `URI cron invalide: %w` | `invalid cron URI: %w` |
| 100 | `le nom du trigger cron est requis...` | `cron trigger name is required...` |
| 152 | `// CronEndpoint représente un endpoint...` | `// CronEndpoint represents a configured cron endpoint` |
| 170 | `// CreateProducer retourne une erreur...` | `// CreateProducer returns an error...` |
| 172 | `le composant cron ne supporte pas les producteurs` | `the cron component does not support producers` |
| 175 | `// CreateConsumer crée un consommateur...` | `// CreateConsumer creates a cron consumer` |
| 187 | `// CronConsumer déclenche le processor...` | `// CronConsumer triggers the processor according to the configured cron expression...` |
| 205 | `// buildSpec construit l'expression...` | `// buildSpec builds the cron expression for robfig/cron` |
| 217 | `SimpleTrigger n'utilise pas robfig/cron` | `SimpleTrigger does not use robfig/cron` |
| 225 | `// Start démarre le consommateur...` | `// Start starts the cron consumer` |
| 237 | `// startCronTrigger enregistre le job...` | `// startCronTrigger registers the job in the shared robfig/cron scheduler` |
| 314 | `// startSimpleTrigger démarre un ticker...` | `// startSimpleTrigger starts a Go ticker for fixed-rate triggering` |
| 315 | `Contrairement à robfig/cron...` | `Unlike robfig/cron, this approach supports sub-second intervals` |
| 348 | `// fireSimpleJob exécute le processor...` | `// fireSimpleJob executes the processor for a SimpleTrigger firing` |
| 396 | `// Stop arrête le consommateur selon...` | `// Stop stops the consumer according to deleteJob/pauseJob options` |
| 401 | `// Mettre en pause : le scheduler continue...` | `// Pause: scheduler continues but jobs become no-ops` |

**Issues:** None

---

### 12. direct_component.go
**French Comments Found:**
| Line | Original French | English Translation |
|------|-----------------|--------------------|
| 10 | `// DirectComponent represents...` | Already English - GOOD! |
| 16 | `// NewDirectComponent creates...` | Already English - GOOD! |

**Note:** This file is already in English! This should be the standard for all files.

**Issues:** None

---

### 13. template_component.go
**French Comments Found:**
| Line | Original French | English Translation |
|------|-----------------|--------------------|
| 16 | `// Constantes de headers pour le composant...` | `// Header constants for the template component` |
| 17-20 | French const definitions | Need translation |
| 42 | `// TemplateComponent représente le composant...` | `// TemplateComponent represents the template component` |
| 48 | `// NewTemplateComponent crée...` | `// NewTemplateComponent creates a new TemplateComponent instance` |
| 55 | `// CreateEndpoint crée un nouvel endpoint...` | `// CreateEndpoint creates a new template endpoint` |
| 56 | `// Format de l'URI: template:chemin...` | `// URI format: template:path/to/file.tmpl[?options]` |
| 60 | `URI template invalide` | `invalid template URI` |
| 74 | `chemin de template manquant dans l'URI` | `template path missing in URI` |
| 99 | `// Option encoding` | (already English) |
| 100 | `// spécifier l'encodage (défaut UTF-8)` | `// specify encoding (default UTF-8)` |
| 106 | `// Option startDelimiter/endDelimiter` | (already English) |
| 117 | `// parseBool parse une chaîne en booléen` | `// parseBool parses a string to boolean` |
| 128 | `// TemplateEndpoint représente un endpoint...` | `// TemplateEndpoint represents a template endpoint` |
| 140 | `// URI retourne l'URI de l'endpoint` | `// URI returns the endpoint URI` |
| 145 | `// CreateProducer crée un producteur template` | `// CreateProducer creates a template producer` |
| 153 | `// CreateConsumer n'est pas supporté...` | `// CreateConsumer is not supported for template component` |
| 155 | `le composant template ne supporte pas les consommateurs` | `the template component does not support consumers` |
| 157 | `// TemplateProducer représente un producteur...` | `// TemplateProducer represents a template producer` |
| 163 | `// Start démarre le producteur template` | `// Start starts the template producer` |
| 176 | `// Stop arrête le producteur template` | `// Stop stops the template producer` |
| 183 | `// Send effectue la transformation du template` | `// Send performs the template transformation` |
| 217 | `erreur lors de l'exécution du template` | `error executing template: %w` |
| 265 | `erreur lors du parsing du template` | `error parsing template: %w` |
| 274 | `erreur lors de l'exécution du template` | `error executing template: %w` |
| 295 | `// prepareTemplateData prépare les données...` | `// prepareTemplateData prepares data for the template` |
| 330 | `// templateFuncs retourne les fonctions...` | `// templateFuncs returns the functions available in templates` |
| 333-339 | French in template functions comments | Need translation |
| 347 | `// Fonction pour échapper JSON...` | `// Function to escape JSON...` |
| 348 | `// Échapper les caractères spéciaux JSON` | `// Escape special JSON characters` |
| 351 | `// Fonction pour marquer du contenu comme sûr` | `// Function to mark content as safe (no HTML escaping)` |
| 373 | `// Fonctions de date` | `// Date functions` |
| 381 | `// Fonctions de type` | `// Type functions` |
| 416 | `// toString convertit n'importe quelle valeur...` | `// toString converts any value to string` |

**Security Issues:**
1. **Line 185-193** - Path traversal: `allowTemplateFromHeader` loads template from header value without sanitization

---

### 14. xslt_component.go
**French Comments Found:**
| Line | Original French | English Translation |
|------|-----------------|--------------------|
| 21 | `// XsltComponent représente le composant XSLT` | `// XsltComponent represents the XSLT component` |
| 24 | `// NewXsltComponent crée une nouvelle instance...` | `// NewXsltComponent creates a new XsltComponent instance` |
| 29 | `// CreateEndpoint crée un nouvel endpoint XSLT` | `// CreateEndpoint creates a new XSLT endpoint` |
| 31 | `// Format de l'URI: xslt:chemin...` | `// URI format: xslt:path/to/file.xsl` |
| 34 | `chemin de fichier manquant dans l'URI` | `file path missing in URI` |
| 44 | `// XsltEndpoint représente un endpoint XSLT` | `// XsltEndpoint represents an XSLT endpoint` |
| 51 | `// URI retourne l'URI de l'endpoint` | `// URI returns the endpoint URI` |
| 56 | `// CreateProducer crée un producteur XSLT` | `// CreateProducer creates an XSLT producer` |
| 63 | `// CreateConsumer n'est pas supporté...` | `// CreateConsumer is not supported...` |
| 68 | `// XsltProducer représente un producteur XSLT` | `// XsltProducer represents an XSLT producer` |
| 74 | `// Start démarre le producteur XSLT` | `// Start starts the XSLT producer` |
| 79-88 | Comment block in French about libxml2 | Need translation |
| 86 | `Réinitialise l'état d'erreur global...` | `Resets global libxml2 error state...` |
| 87 | `pour éviter que des erreurs résiduelles...` | `to prevent residual errors from causing false failures...` |
| 82 | `erreur lors de la lecture du fichier XSLT` | `error reading XSLT file: %v` |
| 91 | `erreur lors du parsing du fichier XSLT` | `error parsing XSLT file: %v` |
| 99 | `// Stop arrête le producteur XSLT` | `// Stop stops the XSLT producer` |
| 108 | `// Send effectue la transformation XSLT` | `// Send performs the XSLT transformation` |
| 112 | `le producteur XSLT n'est pas démarré...` | `XSLT producer is not started or stylesheet is invalid` |
| 118 | `// Récupération du XML à transformer` | `// Retrieving XML to transform` |
| 130 | `erreur lors de la transformation XSLT` | `error during XSLT transformation: %v` |

**Security Issues:**
1. **Line 32** - Path traversal: `tpath := strings.TrimPrefix(uri, "xslt:")` - No validation

---

### 15. xsd_component.go
**French Comments Found:**
| Line | Original French | English Translation |
|------|-----------------|--------------------|
| 12 | `// XsdComponent représente le composant XSD` | `// XsdComponent represents the XSD component` |
| 15 | `// NewXsdComponent crée une nouvelle instance...` | `// NewXsdComponent creates a new XsdComponent instance` |
| 20 | `// CreateEndpoint crée un nouvel endpoint XSD` | `// CreateEndpoint creates a new XSD endpoint` |
| 22 | `// Format de l'URI: xsd:chemin/vers/schema.xsd` | `// URI format: xsd:path/to/schema.xsd` |
| 25 | `chemin de fichier manquant dans l'URI` | `file path missing in URI` |
| 35 | `// XsdEndpoint représente un endpoint XSD` | `// XsdEndpoint represents an XSD endpoint` |
| 42 | `// URI retourne l'URI de l'endpoint` | `// URI returns the endpoint URI` |
| 47 | `// CreateProducer crée un producteur XSD` | `// CreateProducer creates an XSD producer` |
| 54 | `// CreateConsumer n'est pas supporté...` | `// CreateConsumer is not supported...` |
| 59 | `// XsdProducer représente un producteur XSD` | `// XsdProducer represents an XSD producer` |
| 65 | `// Start démarre le producteur XSD` | `// Start starts the XSD producer` |
| 67 | `// Parsing du schéma XSD` | `// Parsing the XSD schema` |
| 70 | `erreur lors du parsing du fichier XSD` | `error parsing XSD file: %v` |
| 76 | `// Stop arrête le producteur XSD` | `// Stop stops the XSD producer` |
| 85 | `// Send effectue la validation XSD` | `// Send performs the XSD validation` |
| 88 | `le producteur XSD n'est pas démarré...` | `XSD producer is not started or schema is invalid` |
| 91 | `// Récupération du XML à valider` | `// Retrieving XML to validate` |
| 99 | `type de corps non supporté...` | `unsupported body type for XSD validation` |
| 105 | `erreur lors du parsing du document XML` | `error parsing XML document: %v` |
| 111 | `erreur de validation XSD` | `XSD validation error: %v` |

**Security Issues:**
1. **Line 23** - Path traversal: `tpath := strings.TrimPrefix(uri, "xsd:")` - No validation

---

### 16. openai_component.go
**French Comments Found:**
| Line | Original French | English Translation |
|------|-----------------|--------------------|
| 11 | `// OpenAIComponent représente le composant OpenAI` | `// OpenAIComponent represents the OpenAI component` |
| 14 | `// NewOpenAIComponent crée une nouvelle instance...` | `// NewOpenAIComponent creates a new OpenAIComponent instance` |
| 19 | `// CreateEndpoint crée un nouvel endpoint OpenAI` | `// CreateEndpoint creates a new OpenAI endpoint` |
| 33 | `// OpenAIEndpoint représente un endpoint OpenAI` | `// OpenAIEndpoint represents an OpenAI endpoint` |
| 40 | `// URI retourne l'URI de l'endpoint` | `// URI returns the endpoint URI` |
| 45 | `// CreateProducer crée un producteur OpenAI` | `// CreateProducer creates an OpenAI producer` |
| 52 | `// CreateConsumer crée un consommateur OpenAI` | `// CreateConsumer creates an OpenAI consumer` |
| 54 | `le composant OpenAI ne supporte pas le mode Consumer...` | `the OpenAI component does not support Consumer mode...` |
| 57 | `// OpenAIProducer représente un producteur OpenAI` | `// OpenAIProducer represents an OpenAI producer` |
| 67 | `authorizationToken (ou apiKey) manquant` | `authorizationToken (or apiKey) missing for OpenAI` |
| 117 | `erreur de requête OpenAI` | `OpenAI request error: %w` |
| 125 | `OpenAI n'a retourné aucune réponse` | `OpenAI returned no response` |

**Issues:** None

---

### 17. exec_component.go
**French Comments Found:**
| Line | Original French | English Translation |
|------|-----------------|--------------------|
| 16 | `// Constantes de headers pour le composant exec` | `// Header constants for the exec component` |
| 17-23 | All header consts have French comments | Need translation |
| 26 | `// ExecComponent représente le composant exec` | `// ExecComponent represents the exec component` |
| 29 | `// NewExecComponent crée une nouvelle instance...` | `// NewExecComponent creates a new ExecComponent instance` |
| 34 | `// CreateEndpoint crée un ExecEndpoint à partir` | `// CreateEndpoint creates an ExecEndpoint from URI` |
| 47 | `l'exécutable est requis dans l'URI exec` | `executable is required in exec URI` |
| 85 | `// ExecEndpoint représente un endpoint de type exec` | `// ExecEndpoint represents an exec endpoint` |
| 96 | `// URI retourne l'URI de l'endpoint` | `// URI returns the endpoint URI` |
| 101 | `// CreateProducer crée un producteur exec` | `// CreateProducer creates an exec producer` |
| 106 | `// CreateConsumer n'est pas supporté...` | `// CreateConsumer is not supported...` |
| 108 | `le composant exec ne supporte pas les consommateurs` | `the exec component does not support consumers` |
| 111 | `// ExecProducer exécute une commande système` | `// ExecProducer executes a system command` |
| 116 | `// Start démarre le producteur exec` | `// Start starts the exec producer` |
| 121 | `// Stop arrête le producteur exec` | `// Stop stops the exec producer` |
| 126 | `// Send exécute la commande et met le résultat...` | `// Send executes the command and puts the result in the exchange` |
| 155 | `// Résolution du timeout (header > URI)` | `// Timeout resolution (header > URI)` |
| 176 | `// Construction du contexte avec timeout...` | `// Building context with optional timeout` |
| 182 | `// Stdin depuis le corps du message` | `// Stdin from message body` |
| 204 | `// Si outFile est défini, lire le résultat...` | `// If outFile is defined, read result from this file` |
| 230 | `exec: timeout dépassé pour la commande` | `exec: timeout exceeded for command %q: %w` |
| 235 | `exec: erreur lors de l'exécution de` | `exec: error executing %q: %w` |

**Security Issues - CRITICAL:**
1. **Line 176** - Command injection vulnerability: `tcmd := exec.CommandContext(ctx, executable, args...)`
   - The `executable` and `targs` are derived from user input (URI parameters, headers, body)
   - No sanitization of the command or arguments
   - An attacker could inject malicious commands

2. **Line 179** - Working directory from user input: `tcmd.tDir = workingDir`
   - Allows changing working directory to arbitrary paths

**Recommendations:**
- Use a whitelist of allowed executables
- Sanitize all input parameters
- Consider using a safer execution model

---

### 18. sql_stored_component.go
**French Comments Found:**
| Line | Original French | English Translation |
|------|-----------------|--------------------|
| 16-26 | Some comments are French in a mostly English file | Mixed translation needed |
| 42-56 | French in struct docstring | Translation needed |

**Security Issues:**
1. **Line 188** - SQL injection via `Interpolate(procedure, exchange)` - Similar to sql_component.go

---

## Recommendations

### Translation Priority (High to Low):
1. **mail_component.go** - Most French comments (~60+)
2. **mongodb_component.go** - Many French constants and comments
3. **cron_component.go** - French in docstrings
4. **sql_component.go** - Several French errors/comments
5. All other files - Various French comments

### Security Priority (Critical to Low):
1. **exec_component.go** - Command injection (CRITICAL)
2. **file_component.go** - Path traversal 
3. **ftp_component.go** - Path traversal
4. **sftp_component.go** - Path traversal
5. **template_component.go** - Path traversal via header
6. **xslt_component.go** - Path traversal
7. **xsd_component.go** - Path traversal
8. **sql_component.go** - SQL injection risk in Interpolate()

## Files to Patch - Summary
Total patches needed: ~125+ French comment translations across 18 files

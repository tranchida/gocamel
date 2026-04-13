package gocamel

import (
	"net/url"
	"strconv"
	"strings"
	"time"
)

// FileExistBehavior définit le comportement du producer lorsque le fichier cible existe déjà.
// Correspond à l'option fileExist d'Apache Camel.
type FileExistBehavior string

const (
	// FileExistOverride écrase le fichier existant (défaut).
	FileExistOverride FileExistBehavior = "Override"
	// FileExistAppend ajoute le contenu à la suite du fichier existant.
	FileExistAppend FileExistBehavior = "Append"
	// FileExistFail retourne une erreur si le fichier existe déjà.
	FileExistFail FileExistBehavior = "Fail"
	// FileExistIgnore ignore silencieusement l'écriture si le fichier existe déjà.
	FileExistIgnore FileExistBehavior = "Ignore"
)

// PollingOptions regroupe les paramètres URI communs aux consumers à base de polling
// (FTP, SFTP, SMB). Correspond aux options GenericFile consumer d'Apache Camel.
type PollingOptions struct {
	// Delay entre deux cycles de poll (défaut : 5s).
	Delay time.Duration
	// InitialDelay avant le premier poll (défaut : 1s).
	InitialDelay time.Duration
	// MaxMessagesPerPoll limite le nombre de fichiers traités par cycle ; 0 = illimité.
	MaxMessagesPerPoll int
	// Noop empêche toute action post-traitement (delete/move) sur le fichier.
	Noop bool
	// Delete supprime le fichier distant après traitement réussi.
	Delete bool
	// Move déplace le fichier vers ce répertoire distant après traitement réussi.
	Move string
	// MoveFailed déplace le fichier vers ce répertoire distant en cas d'erreur de traitement.
	MoveFailed string
	// Recursive descend dans les sous-répertoires.
	Recursive bool
	// Include est une regex que les noms de fichiers doivent satisfaire pour être traités.
	Include string
	// Exclude est une regex ; les noms correspondants sont ignorés.
	Exclude string
}

// ParsePollingOptions lit les options consumer de polling depuis une URI parsée.
func ParsePollingOptions(u *url.URL) PollingOptions {
	opts := PollingOptions{
		Delay:        5 * time.Second,
		InitialDelay: 1 * time.Second,
	}
	if s := GetConfigValue(u, "delay"); s != "" {
		if d, err := time.ParseDuration(s); err == nil {
			opts.Delay = d
		}
	}
	if s := GetConfigValue(u, "initialDelay"); s != "" {
		if d, err := time.ParseDuration(s); err == nil {
			opts.InitialDelay = d
		}
	}
	if s := GetConfigValue(u, "maxMessagesPerPoll"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			opts.MaxMessagesPerPoll = n
		}
	}
	opts.Noop = strings.EqualFold(GetConfigValue(u, "noop"), "true")
	opts.Delete = strings.EqualFold(GetConfigValue(u, "delete"), "true")
	opts.Move = GetConfigValue(u, "move")
	opts.MoveFailed = GetConfigValue(u, "moveFailed")
	opts.Recursive = strings.EqualFold(GetConfigValue(u, "recursive"), "true")
	opts.Include = GetConfigValue(u, "include")
	opts.Exclude = GetConfigValue(u, "exclude")
	return opts
}

// ParseFileExist lit l'option fileExist producer depuis une URI parsée.
// Retourne FileExistOverride si absent ou non reconnu.
func ParseFileExist(u *url.URL) FileExistBehavior {
	switch FileExistBehavior(GetConfigValue(u, "fileExist")) {
	case FileExistAppend:
		return FileExistAppend
	case FileExistFail:
		return FileExistFail
	case FileExistIgnore:
		return FileExistIgnore
	default:
		return FileExistOverride
	}
}

// parseConnectTimeout lit le connectTimeout depuis une URI parsée (défaut : 10s).
func parseConnectTimeout(u *url.URL) time.Duration {
	if s := GetConfigValue(u, "connectTimeout"); s != "" {
		if d, err := time.ParseDuration(s); err == nil {
			return d
		}
	}
	return 10 * time.Second
}

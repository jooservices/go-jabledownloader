package app

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"go.opentelemetry.io/otel/attribute"

	"github.com/jooservices/go-jabledownloader/internal/domain"
	"github.com/jooservices/go-jabledownloader/internal/engine"
	"github.com/jooservices/go-jabledownloader/internal/site"
)

// imageExtRe accepts the short, plain extensions image URLs use.
var imageExtRe = regexp.MustCompile(`^\.[a-z0-9]{1,5}$`)

// getGallery saves every photo of a gallery into its layout directory,
// one file per photo through the progressive engine. Photos already saved
// are skipped (unless --force), so an interrupted run resumes.
func (s *Service) getGallery(ctx context.Context, siteName string, gs site.GallerySite, input string) error {
	ctx, end := s.Tel.StartSpan(ctx, "gallery.download", attribute.String("site", siteName))
	defer end()

	gallery, err := gs.Gallery(ctx, input)
	if err != nil {
		return fmt.Errorf("fetch gallery: %w", err)
	}
	dir, err := s.layout().VideoDir(siteName, gallery.Code)
	if err != nil {
		return err
	}
	total := len(gallery.Photos)
	s.emit(GalleryResolved{Site: siteName, Code: gallery.Code, Title: gallery.Title, Dir: dir, Photos: total})
	if s.Opts.DryRun {
		s.emit(DryRun{})
		return nil
	}
	eng, err := engine.For(domain.SourceProgressive)
	if err != nil {
		return err
	}

	summary := GalleryDownloaded{Code: gallery.Code, Dir: dir}
	for i, photo := range gallery.Photos {
		name := photoFileName(i, photo)
		target := filepath.Join(dir, name)
		if info, err := os.Stat(target); err == nil && info.Size() > 0 && !s.Opts.Force {
			summary.Skipped++
			s.emit(PhotoSaved{Index: i + 1, Total: total, Path: target, Size: info.Size(), Skipped: true})
			continue
		}
		result, err := eng.Download(ctx, engine.Request{
			Source:   domain.Source{Kind: domain.SourceProgressive, URL: photo.URL, Headers: photo.Headers},
			Dir:      dir,
			FileName: name,
			Workers:  1,
		}, nil)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			summary.Failed++
			s.emit(ItemFailed{Code: name, Err: err})
			continue
		}
		summary.Saved++
		summary.Size += result.Size
		s.emit(PhotoSaved{Index: i + 1, Total: total, Path: result.Path, Size: result.Size})
	}
	s.emit(summary)
	s.Tel.Count(ctx, "gallery.photos", int64(summary.Saved), attribute.String("site", siteName), attribute.String("outcome", "ok"))
	s.Tel.Count(ctx, "gallery.photos", int64(summary.Failed), attribute.String("site", siteName), attribute.String("outcome", "fail"))
	if summary.Failed > 0 {
		return &PlanError{Failed: summary.Failed}
	}
	return nil
}

// photoFileName numbers photos in gallery order ("007-<id>.jpg"). The id
// comes from an untrusted page, so it is used only when it is a safe name.
func photoFileName(i int, photo domain.Photo) string {
	ext := strings.ToLower(path.Ext(photo.URL))
	if !imageExtRe.MatchString(ext) {
		ext = ".jpg"
	}
	if domain.ValidCode(photo.ID) {
		return fmt.Sprintf("%03d-%s%s", i+1, photo.ID, ext)
	}
	return fmt.Sprintf("%03d%s", i+1, ext)
}

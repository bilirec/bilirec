package convert_test

import (
	"os"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/services/convert"
	"github.com/bilirec/bilirec/utils"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

func TestCloudConvert(t *testing.T) {

	if os.Getenv("CLOUDCONVERT_API_KEY") == "" {
		t.Skip("CLOUDCONVERT_API_KEY not set, skipping test")
	}

	os.Setenv("CLOUDCONVERT_THRESHOLD", "0") // force cloud convert

	var svc *convert.Service
	app := fxtest.New(t,
		config.Module,
		fx.Provide(convert.NewService),
		fx.Populate(&svc),
	)

	app.RequireStart()
	defer app.RequireStop()

	q, err := svc.Enqueue("input.flv", "mp4", false)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Enqueued convert job: %v", utils.PrettyPrintJSON(q))
}

func TestUntilCloudConvertCompleted(t *testing.T) {
	if os.Getenv("CLOUDCONVERT_API_KEY") == "" {
		t.Skip("CLOUDCONVERT_API_KEY not set, skipping test")
	}

	var svc *convert.Service
	app := fxtest.New(t,
		config.Module,
		fx.Provide(convert.NewService),
		fx.Populate(&svc),
	)

	app.RequireStart()
	defer app.RequireStop()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	// wait worker finish
	for {
		select {
		case <-t.Context().Done():
			t.Skip("service context cancelled")
		case <-ticker.C:
			if f, err := os.Stat("input.mp4"); err != nil && os.IsNotExist(err) {
				t.Log("conversion not completed yet...")
			} else if err != nil {
				t.Fatal(err)
			} else if f.Size() > 0 {
				t.Logf("conversion completed, output file size: %d bytes", f.Size())
				return
			} else {
				t.Log("output file size is 0, wait longer...")
			}
		}
	}
}

func TestCancelUnExistingCloudConvertTask(t *testing.T) {
	if os.Getenv("CLOUDCONVERT_API_KEY") == "" {
		t.Skip("CLOUDCONVERT_API_KEY not set, skipping test")
	}

	var svc *convert.Service

	app := fxtest.New(t,
		config.Module,
		fx.Provide(convert.NewService),
		fx.Populate(&svc),
	)

	app.RequireStart()
	defer app.RequireStop()

	uuid, err := utils.NewUUIDv4()
	if err != nil {
		t.Fatal(err)
	}
	err = svc.Cancel(uuid)
	if err == nil {

	}
}

func init() {
	os.Setenv("DEBUG", "true")
}

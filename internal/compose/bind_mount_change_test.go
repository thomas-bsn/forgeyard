package compose

import "testing"

func TestMergeBindMountsKeepsNamedVolumesInheritedFromAnchors(t *testing.T) {
	current := `x-worker: &worker
  volumes:
    - data:/var/lib/data
services:
  web:
    image: nginx:alpine
    volumes:
      - /srv/old:/data:ro
  worker:
    <<: *worker
    image: busybox:1.36
volumes:
  data:
`
	proposal := `x-worker: &worker
  volumes:
    - data:/var/lib/data
services:
  web:
    image: nginx:alpine
    volumes:
      - /srv/new:/data:rw
  worker:
    <<: *worker
    image: busybox:1.36
volumes:
  data:
`
	merged, err := MergeBindMounts(current, proposal, []BindMountChange{{Service: "web", Source: "/srv/new", Target: "/data", ReadOnly: false}})
	if err != nil {
		t.Fatal(err)
	}
	stack, errs := Parse([]byte(merged), nil)
	if errs != nil {
		t.Fatal(errs)
	}
	var worker, web *Service
	for i := range stack.Services {
		switch stack.Services[i].Name {
		case "worker":
			worker = &stack.Services[i]
		case "web":
			web = &stack.Services[i]
		}
	}
	if worker == nil || len(worker.Volumes) != 1 || worker.Volumes[0].Volume != "data" || worker.Volumes[0].Target != "/var/lib/data" {
		t.Fatalf("inherited named volume was lost: %+v", worker)
	}
	if web == nil || len(web.Volumes) != 1 || web.Volumes[0].Source != "/srv/new" || web.Volumes[0].ReadOnly {
		t.Fatalf("approved bind mount was not merged: %+v", web)
	}
}

func TestMergeBindMountsMaterializesAliasedServices(t *testing.T) {
	current := `x-web: &web
  image: nginx:alpine
  volumes:
    - /srv/old:/data:ro
services:
  web: *web
`
	proposal := `x-web: &web
  image: nginx:alpine
  volumes:
    - /srv/new:/data:rw
services:
  web: *web
`
	merged, err := MergeBindMounts(current, proposal, []BindMountChange{{Service: "web", Source: "/srv/new", Target: "/data", ReadOnly: false}})
	if err != nil {
		t.Fatal(err)
	}
	stack, errs := Parse([]byte(merged), nil)
	if errs != nil {
		t.Fatal(errs)
	}
	if len(stack.Services) != 1 || len(stack.Services[0].Volumes) != 1 || stack.Services[0].Volumes[0].Source != "/srv/new" || stack.Services[0].Volumes[0].ReadOnly {
		t.Fatalf("approved bind mount was lost from aliased service: %+v", stack.Services)
	}
}
